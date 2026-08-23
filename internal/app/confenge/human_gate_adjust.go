package confenge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

// Derivation taxonomy for confenge_cohort_versions. It is durable in Postgres
// (000117) because "there is a version N+1" cannot, by itself, tell an auditor
// whether the bytes are identical, recomposed by machine, or edited by a human.
const (
	// DerivationCreate is the first freeze of a cohort from a canonical run.
	DerivationCreate = "CREATE"
	// DerivationReproduce copies frozen_manifest byte-for-byte.
	DerivationReproduce = "REPRODUCE"
	// DerivationRecompose is reserved: re-run the composer against the current
	// source, policy and composer version. Not implemented by this contract.
	DerivationRecompose = "RECOMPOSE"
	// DerivationAdjust is a human copy edit on exactly one candidate.
	DerivationAdjust = "ADJUST"
)

// HumanGateAdjustMinReason is the shortest reason that can justify overriding
// composed copy. A one-word reason is not an audit trail.
const HumanGateAdjustMinReason = 8

// humanGateImmutableFields are the facts an operator may never "fix" by typing.
// Each one is either canonical-source evidence or a machine-derived binding;
// changing it in a copy edit would produce a manifest that claims provenance it
// does not have. Correction is a source correction plus a fresh composition.
var humanGateImmutableFields = []string{
	"mailbox",
	"recipient",
	"evidence",
	"source",
	"policy_version",
	"route_class",
	"composer_version",
}

// HumanGateAdjustInput is one human copy edit on one candidate of one
// immutable version. Subject and body are the only mutable facts.
type HumanGateAdjustInput struct {
	Subject            string `json:"subject"`
	BodyText           string `json:"body_text"`
	Reason             string `json:"reason"`
	Confirmation       string `json:"confirmation"`
	ExpectedFrozenHash string `json:"expected_frozen_hash"`

	// RawBody is the exact request body. It is how the server detects an
	// attempt to set an immutable field: a typed struct silently drops
	// unknown keys, and silently dropping "mailbox" is how a caller comes to
	// believe it changed the recipient.
	RawBody        []byte `json:"-"`
	IdempotencyKey string `json:"-"`
	CorrelationID  string `json:"-"`
}

// HumanGateAdjustmentDiffEntry is one field the human changed.
type HumanGateAdjustmentDiffEntry struct {
	Field  string `json:"field"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// HumanGateAdjustment is the durable per-candidate audit of a copy edit.
type HumanGateAdjustment struct {
	ID                     uuid.UUID                      `json:"id"`
	CohortID               uuid.UUID                      `json:"cohort_id"`
	FromVersion            int                            `json:"from_version"`
	ToVersion              int                            `json:"to_version"`
	CandidateID            uuid.UUID                      `json:"candidate_id"`
	BeforeContentHash      string                         `json:"before_content_hash"`
	AfterContentHash       string                         `json:"after_content_hash"`
	BeforeFrozenHash       string                         `json:"before_frozen_hash"`
	AfterFrozenHash        string                         `json:"after_frozen_hash"`
	Diff                   []HumanGateAdjustmentDiffEntry `json:"diff"`
	RevokedAuthorizationID *uuid.UUID                     `json:"revoked_authorization_id"`
	ActorID                uuid.UUID                      `json:"actor_id"`
	CorrelationID          string                         `json:"correlation_id"`
	Receipt                string                         `json:"receipt"`
	CreatedAt              time.Time                      `json:"created_at"`

	// reason is durable in confenge_cohort_adjustments but deliberately not in
	// the v1 response body; the contract's adjustment envelope is fixed.
	reason string
}

// HumanGateAdjustResult is the 201 body: the whole new version plus the receipt
// that explains how it differs from its parent.
type HumanGateAdjustResult struct {
	ContractVersion string              `json:"contract_version"`
	Cohort          *HumanGateCohort    `json:"cohort"`
	Adjustment      HumanGateAdjustment `json:"adjustment"`
}

// humanGateImmutableField reports the first protected key present in the raw
// body, or "". Keys are inspected in sorted order so the same body always
// names the same field.
func humanGateImmutableField(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(raw, &body) != nil {
		return ""
	}
	keys := make([]string, 0, len(body))
	for k := range body {
		keys = append(keys, strings.ToLower(strings.TrimSpace(k)))
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, protected := range humanGateImmutableFields {
			if k == protected || strings.HasPrefix(k, protected+"_") {
				return k
			}
		}
	}
	return ""
}

func humanGateImmutableFieldError(field string) *errx.Error {
	return humanGateError(errx.Unprocessable, "immutable_field", fmt.Sprintf(
		"%q is not correctable here: recipient, evidence, source, policy, route class and composer version are bound to the canonical source and to the composition that produced this version. Correct the canonical source, then compose a fresh cohort. Adjust may only change subject and body_text.",
		field))
}

// AdjustHumanGateCandidate creates version N+1 of the same cohort carrying a
// human copy edit for exactly one candidate.
//
// It never updates the parent row: the prior frozen_manifest stays byte
// identical and readable forever. The new version is born with no validation,
// no review and no GO, because nothing that was approved about the old copy is
// evidence about the new copy. It queues nothing, dispatches nothing, sends
// nothing and resumes nothing.
func (s *service) AdjustHumanGateCandidate(ctx context.Context, orgID, actorID, versionID, candidateID uuid.UUID, in HumanGateAdjustInput) (*HumanGateAdjustResult, *errx.Error) {
	if s.humanGateDB == nil {
		return nil, humanGateError(errx.ServiceUnavailable, "human_gate_store_unavailable", "human gate store is not configured")
	}
	if actorID == uuid.Nil {
		return nil, errx.ErrUnauthorized
	}
	// The immutable-field refusal runs before any other validation: a caller
	// that tried to retype a mailbox must be told that, not that its reason
	// was too short.
	if field := humanGateImmutableField(in.RawBody); field != "" {
		return nil, humanGateImmutableFieldError(field)
	}

	subject := strings.TrimSpace(in.Subject)
	body := strings.TrimSpace(in.BodyText)
	reason := strings.TrimSpace(in.Reason)
	confirmation := strings.ToLower(strings.TrimSpace(in.Confirmation))
	expected := strings.TrimSpace(in.ExpectedFrozenHash)
	key := strings.TrimSpace(in.IdempotencyKey)

	if key == "" {
		return nil, humanGateError(errx.BadRequest, "idempotency_key_required", "Idempotency-Key is required")
	}
	if subject == "" || body == "" {
		return nil, humanGateError(errx.BadRequest, "adjust_copy_required", "subject and body_text are required and cannot be empty")
	}
	if len([]rune(reason)) < HumanGateAdjustMinReason {
		return nil, humanGateError(errx.BadRequest, "adjust_reason_required", fmt.Sprintf("reason is required and must be at least %d characters", HumanGateAdjustMinReason))
	}
	if confirmation == "" {
		return nil, humanGateError(errx.BadRequest, "adjust_confirmation_required", "confirmation must name the immutable version being adjusted, for example \"v1\"")
	}
	if expected == "" {
		return nil, humanGateError(errx.BadRequest, "adjust_expected_frozen_hash_required", "expected_frozen_hash is required")
	}

	unlock, lockErr := s.lockHumanGateIntent(ctx, orgID, key)
	if lockErr != nil {
		return nil, lockErr
	}
	defer unlock()

	reqHash := humanGateRequestHash(struct {
		V, C           uuid.UUID
		S, B, R, F, Cf string
	}{versionID, candidateID, subject, body, reason, expected, confirmation})

	if replay, x := s.humanGateAdjustmentByIdempotency(ctx, orgID, key, reqHash); replay != nil || x != nil {
		return replay, x
	}

	now := time.Now().UTC()
	parent, x := s.GetHumanGateCohort(ctx, orgID, versionID, now)
	if x != nil {
		return nil, x
	}
	if expected != parent.FrozenHash {
		return nil, humanGateError(errx.Conflict, "frozen_hash_mismatch", "expected_frozen_hash does not match the frozen hash of this version; re-read the version before adjusting it")
	}
	if !humanGateVersionConfirmed(confirmation, parent.Version) {
		return nil, humanGateError(errx.Conflict, "confirmation_mismatch", fmt.Sprintf("confirmation must be exactly \"v%d\"", parent.Version))
	}
	if parent.Freshness != "FRESH" {
		return nil, humanGateError(errx.Conflict, "source_stale", "canonical source evidence for this version is stale; adjust cannot refresh evidence")
	}
	// Recomputing ContentHash uses the same freeze-path helper, which binds the
	// current ComposerVersion. Rebasing a hash onto a composer that did not
	// produce the manifest would silently invent a second source of truth.
	if strings.TrimSpace(parent.Manifest.ComposerVersion) != ComposerVersion {
		return nil, humanGateError(errx.Conflict, "composer_drift", "this version was composed by another composer version; recompose before adjusting")
	}
	if n := len(parent.Manifest.Members); n < 1 || n > HumanGateMaxCohort {
		return nil, humanGateError(errx.Unprocessable, "cohort_bounds_violation", fmt.Sprintf("cohort holds %d members; the bounded cohort is 1..%d", n, HumanGateMaxCohort))
	}
	if parent.Manifest.MaxDailyVolume < 1 || parent.Manifest.MaxDailyVolume > DefaultCohortDispatchCap {
		return nil, humanGateError(errx.Unprocessable, "cohort_bounds_violation", fmt.Sprintf("max_daily_volume %d is outside the bounded cohort cap 1..%d", parent.Manifest.MaxDailyVolume, DefaultCohortDispatchCap))
	}

	// Deep-copy the parent manifest. The parent row is never written again;
	// this is a new artifact that happens to start from the old bytes.
	next, err := humanGateCloneManifest(&parent.Manifest)
	if err != nil {
		return nil, humanGateError(errx.Internal, "cohort_read_failed", "cohort version could not be read")
	}
	idx := -1
	for i := range next.Members {
		if next.Members[i].CandidateID == candidateID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, humanGateError(errx.NotFound, "candidate_not_found", "candidate is not in this immutable version")
	}
	member := &next.Members[idx]
	beforeSubject, beforeBody, beforeContentHash := member.Subject, member.BodyText, member.ContentHash

	// Live candidate is best effort: it only gives copy QA the same recipient
	// context the freeze path had. Live suppression/opt-out/bounce are gates on
	// GO and on dispatch, not on editing a draft, and are re-read on every GET.
	live := s.humanGateLiveCandidate(ctx, orgID, parent, member.AccountID, candidateID)
	if qa := ValidateCopyForRouteClass(member.RouteClass, body, subject, live); len(qa) > 0 {
		sort.Strings(qa)
		return nil, humanGateError(errx.Unprocessable, "copy_qa_failed", "adjusted copy failed copy QA: "+strings.Join(qa, ","))
	}

	member.Subject = subject
	member.BodyText = body
	member.ContentHash = hashControlledContent(member.Mailbox, member.RouteClass, subject, body)

	// Every derived value is recomputed with the freeze path's own helpers. A
	// second hashing implementation is a second source of truth.
	mailboxes := make([]string, 0, len(next.Members))
	for i := range next.Members {
		mailboxes = append(mailboxes, next.Members[i].Mailbox)
	}
	next.RecipientSetHash = HashRecipientSet(mailboxes)
	next.Preview.SamplesByClass = selectPreviewSamples(next.Members, previewSamplePerClass)
	// N+1 is born with no authority. Carrying the parent's authorization id
	// into a manifest that nobody authorized is exactly the two-live-authority
	// bug this operation exists to avoid.
	next.AuthorizationID = uuid.Nil
	next.RealEmailSent = false
	next.AutoSendEnabled = false
	next.GreenAutorunEnabled = false
	next.Warnings = humanGateAppendWarning(next.Warnings, "human_adjusted_copy:"+candidateID.String())
	next.CohortHash = HashFrozenCohort(next)
	next.CohortID = deriveCohortID(next.SnapshotHash, next.FeedIdentity, next.CohortHash)

	adjustment := HumanGateAdjustment{
		ID:                uuid.New(),
		CohortID:          parent.CohortID,
		FromVersion:       parent.Version,
		CandidateID:       candidateID,
		BeforeContentHash: beforeContentHash,
		AfterContentHash:  member.ContentHash,
		BeforeFrozenHash:  parent.FrozenHash,
		AfterFrozenHash:   next.CohortHash,
		Diff: []HumanGateAdjustmentDiffEntry{
			{Field: "subject", Before: beforeSubject, After: subject},
			{Field: "body_text", Before: beforeBody, After: body},
		},
		ActorID:       actorID,
		CorrelationID: strings.TrimSpace(in.CorrelationID),
		CreatedAt:     now,
		reason:        reason,
	}
	adjustment.Receipt = humanGateReceipt("adjustment", adjustment.ID)

	newVersionID, toVersion, revoked, x := s.humanGateCommitAdjust(ctx, orgID, actorID, versionID, parent, next, &adjustment, key, reqHash)
	if x != nil {
		if replay, rx := s.humanGateAdjustmentByIdempotency(ctx, orgID, key, reqHash); replay != nil || rx != nil {
			return replay, rx
		}
		return nil, x
	}
	adjustment.ToVersion = toVersion
	adjustment.RevokedAuthorizationID = revoked

	s.auditHumanGate(ctx, orgID, actorID, "cohort_candidate_copy_adjusted", newVersionID,
		map[string]string{"version": fmt.Sprint(parent.Version), "frozen_hash": parent.FrozenHash, "content_hash": beforeContentHash},
		map[string]string{"version": fmt.Sprint(toVersion), "frozen_hash": next.CohortHash, "content_hash": member.ContentHash, "derivation": DerivationAdjust})

	cohort, x := s.GetHumanGateCohort(ctx, orgID, newVersionID, time.Now().UTC())
	if x != nil {
		return nil, x
	}
	cohort.OperationReceipt = adjustment.Receipt
	return &HumanGateAdjustResult{ContractVersion: HumanGateContractV1, Cohort: cohort, Adjustment: adjustment}, nil
}

func humanGateAppendWarning(warnings []string, w string) []string {
	for _, existing := range warnings {
		if existing == w {
			return warnings
		}
	}
	return append(append([]string{}, warnings...), w)
}

// humanGateCloneManifest round-trips through JSON so the new manifest shares no
// slice or map backing array with the parent's in-memory copy.
func humanGateCloneManifest(in *FrozenCohortSnapshot) (*FrozenCohortSnapshot, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	var out FrozenCohortSnapshot
	if err = json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *service) humanGateLiveCandidate(ctx context.Context, orgID uuid.UUID, v *HumanGateCohort, accountID, candidateID uuid.UUID) *models.OutreachContactCandidate {
	if s.repo == nil || v == nil {
		return nil
	}
	accounts, err := AccountsFromOrgForRun(ctx, s.repo, orgID, v.Source, v.SourceRunID)
	if err != nil {
		return nil
	}
	for i := range accounts {
		if accounts[i].Account.ID != accountID {
			continue
		}
		for j := range accounts[i].Candidates {
			if accounts[i].Candidates[j].ID == candidateID {
				return &accounts[i].Candidates[j]
			}
		}
	}
	return nil
}

// humanGateCommitAdjust is the whole side effect, in one transaction:
// lock the parent, prove it is still the latest version, revoke any live
// authority it carries, insert N+1, insert the audit row. Nothing here updates
// the parent's frozen_manifest.
func (s *service) humanGateCommitAdjust(ctx context.Context, orgID, actorID, versionID uuid.UUID, parent *HumanGateCohort, next *FrozenCohortSnapshot, adjustment *HumanGateAdjustment, key, reqHash string) (uuid.UUID, int, *uuid.UUID, *errx.Error) {
	tx, err := s.humanGateDB.Begin(ctx)
	if err != nil {
		return uuid.Nil, 0, nil, humanGateError(errx.ServiceUnavailable, "human_gate_store_unavailable", "human gate store is not available")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var lockedVersion int
	var lockedHash string
	err = tx.QueryRow(ctx, `SELECT version,frozen_hash FROM confenge_cohort_versions WHERE id=$1 AND organization_id=$2 FOR UPDATE`, versionID, orgID).Scan(&lockedVersion, &lockedHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, 0, nil, humanGateError(errx.NotFound, "cohort_version_not_found", "cohort version was not found")
	}
	if err != nil {
		return uuid.Nil, 0, nil, humanGateError(errx.ServiceUnavailable, "human_gate_read_failed", "cohort version could not be locked")
	}
	if lockedHash != parent.FrozenHash || lockedVersion != parent.Version {
		return uuid.Nil, 0, nil, humanGateError(errx.Conflict, "frozen_hash_mismatch", "this version changed while the adjustment was being prepared; re-read it")
	}

	var maxVersion int
	if err = tx.QueryRow(ctx, `SELECT COALESCE(max(version),0) FROM confenge_cohort_versions WHERE organization_id=$1 AND cohort_id=$2`, orgID, parent.CohortID).Scan(&maxVersion); err != nil {
		return uuid.Nil, 0, nil, humanGateError(errx.ServiceUnavailable, "human_gate_read_failed", "cohort versions could not be read")
	}
	if maxVersion != lockedVersion {
		return uuid.Nil, 0, nil, humanGateError(errx.Conflict, "version_superseded", fmt.Sprintf("version %d is no longer the latest version of this cohort (latest is %d); re-read it before adjusting", lockedVersion, maxVersion))
	}

	revoked, x := humanGateRevokePriorAuthority(ctx, tx, orgID, actorID, versionID, adjustment.reason)
	if x != nil {
		return uuid.Nil, 0, nil, x
	}

	toVersion := lockedVersion + 1
	manifest, err := json.Marshal(next)
	if err != nil {
		return uuid.Nil, 0, nil, humanGateError(errx.Internal, "cohort_store_failed", "adjusted manifest could not be encoded")
	}
	newVersionID := uuid.New()
	_, err = tx.Exec(ctx, `INSERT INTO confenge_cohort_versions
		(id,organization_id,cohort_id,version,source_run_id,source_system,source_as_of,freshness_expires_at,policy_version,frozen_hash,frozen_manifest,derivation,parent_version,created_by,correlation_id,idempotency_key,request_hash)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
		newVersionID, orgID, parent.CohortID, toVersion, parent.SourceRunID, parent.Source, parent.AsOf, parent.FreshUntil,
		parent.PolicyVersion, next.CohortHash, manifest, DerivationAdjust, lockedVersion, actorID,
		adjustment.CorrelationID, key, reqHash)
	if err != nil {
		return uuid.Nil, 0, nil, humanGateAdjustWriteError(err, "cohort_store_failed", "adjusted cohort version could not be stored")
	}

	diff, err := json.Marshal(adjustment.Diff)
	if err != nil {
		return uuid.Nil, 0, nil, humanGateError(errx.Internal, "cohort_store_failed", "adjustment diff could not be encoded")
	}
	_, err = tx.Exec(ctx, `INSERT INTO confenge_cohort_adjustments
		(id,organization_id,cohort_id,from_version_id,to_version_id,from_version,to_version,candidate_id,before_content_hash,after_content_hash,before_frozen_hash,after_frozen_hash,diff,reason,revoked_authorization_id,actor_id,correlation_id,receipt,idempotency_key,request_hash,created_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)`,
		adjustment.ID, orgID, parent.CohortID, versionID, newVersionID, lockedVersion, toVersion, adjustment.CandidateID,
		adjustment.BeforeContentHash, adjustment.AfterContentHash, adjustment.BeforeFrozenHash, adjustment.AfterFrozenHash,
		diff, adjustment.reason, revoked, actorID, adjustment.CorrelationID, adjustment.Receipt, key, reqHash, adjustment.CreatedAt)
	if err != nil {
		return uuid.Nil, 0, nil, humanGateAdjustWriteError(err, "adjustment_store_failed", "adjustment receipt could not be stored")
	}

	if err = tx.Commit(ctx); err != nil {
		return uuid.Nil, 0, nil, humanGateAdjustWriteError(err, "adjustment_store_failed", "adjustment could not be committed")
	}
	return newVersionID, toVersion, revoked, nil
}

func humanGateAdjustWriteError(err error, id, message string) *errx.Error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		switch pgErr.ConstraintName {
		case "confenge_cohort_versions_identity", "confenge_cohort_adjustments_target":
			return humanGateError(errx.Conflict, "version_superseded", "another adjustment created the next version first; re-read the cohort")
		case "confenge_cohort_versions_idempotency", "confenge_cohort_adjustments_idempotency":
			return humanGateError(errx.Conflict, "idempotency_payload_conflict", "Idempotency-Key was already used with another payload")
		}
		return humanGateError(errx.Conflict, "version_superseded", "the cohort changed concurrently; re-read it")
	}
	return humanGateError(errx.Internal, id, message)
}

// humanGateRevokePriorAuthority revokes, inside the caller's transaction, any
// live bounded authority the parent version carries. There must never be two
// valid authorities over the same cohort. If the authority cannot be proven
// revoked inside this transaction, the adjustment is refused instead.
func humanGateRevokePriorAuthority(ctx context.Context, tx pgx.Tx, orgID, actorID, versionID uuid.UUID, reason string) (*uuid.UUID, *errx.Error) {
	var decision string
	var authorizationID *uuid.UUID
	err := tx.QueryRow(ctx, `SELECT decision,authorization_id FROM confenge_cohort_go_decisions
		WHERE organization_id=$1 AND cohort_version_id=$2 ORDER BY created_at DESC,id DESC LIMIT 1`, orgID, versionID).Scan(&decision, &authorizationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, humanGateError(errx.Conflict, "authority_active", "the prior version's authorization state could not be read; adjust refuses rather than risk two live authorities")
	}
	if decision != "GO" || authorizationID == nil || *authorizationID == uuid.Nil {
		return nil, nil
	}

	var reg *string
	if err = tx.QueryRow(ctx, `SELECT to_regclass('confenge_bounded_cohort_authorizations')::text`).Scan(&reg); err != nil || reg == nil {
		return nil, humanGateError(errx.Conflict, "authority_active", "version carries a GO authorization that cannot be revoked in this transaction; revoke it explicitly before adjusting")
	}

	now := time.Now().UTC()
	var revokedAt *time.Time
	var expiresAt time.Time
	err = tx.QueryRow(ctx, `SELECT revoked_at,expires_at FROM confenge_bounded_cohort_authorizations
		WHERE id=$1 AND organization_id=$2 FOR UPDATE`, *authorizationID, orgID).Scan(&revokedAt, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// The GO receipt names an authority that no longer exists. Nothing can
		// dispatch through it, so there is nothing to revoke.
		return nil, nil
	}
	if err != nil {
		return nil, humanGateError(errx.Conflict, "authority_active", "the prior version's authorization could not be locked; adjust refuses rather than risk two live authorities")
	}
	if revokedAt != nil || !now.Before(expiresAt.UTC()) {
		return nil, nil
	}
	tag, err := tx.Exec(ctx, `UPDATE confenge_bounded_cohort_authorizations
		SET revoked_at=$2, revoke_actor=$3, revoke_reason=$4, updated_at=$2
		WHERE id=$1 AND organization_id=$5 AND revoked_at IS NULL`, *authorizationID, now, actorID, "human_gate_adjust:"+reason, orgID)
	if err != nil || tag.RowsAffected() != 1 {
		return nil, humanGateError(errx.Conflict, "authority_active", "the prior version's authorization could not be atomically revoked; revoke it explicitly, then adjust")
	}
	return authorizationID, nil
}

// humanGateAdjustmentByIdempotency replays a stored adjustment. A replay must
// return the same adjustment and must not create a second version.
func (s *service) humanGateAdjustmentByIdempotency(ctx context.Context, orgID uuid.UUID, key, reqHash string) (*HumanGateAdjustResult, *errx.Error) {
	var stored string
	var toVersionID uuid.UUID
	var a HumanGateAdjustment
	var diff []byte
	err := s.humanGateDB.QueryRow(ctx, `SELECT id,cohort_id,to_version_id,from_version,to_version,candidate_id,before_content_hash,after_content_hash,before_frozen_hash,after_frozen_hash,diff,reason,revoked_authorization_id,actor_id,correlation_id,receipt,created_at,request_hash
		FROM confenge_cohort_adjustments WHERE organization_id=$1 AND idempotency_key=$2`, orgID, key).
		Scan(&a.ID, &a.CohortID, &toVersionID, &a.FromVersion, &a.ToVersion, &a.CandidateID, &a.BeforeContentHash, &a.AfterContentHash,
			&a.BeforeFrozenHash, &a.AfterFrozenHash, &diff, &a.reason, &a.RevokedAuthorizationID, &a.ActorID, &a.CorrelationID, &a.Receipt, &a.CreatedAt, &stored)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, humanGateError(errx.Internal, "idempotency_read_failed", "idempotency receipt could not be read")
	}
	if stored != reqHash {
		return nil, humanGateError(errx.Conflict, "idempotency_payload_conflict", "Idempotency-Key was already used with another payload")
	}
	if json.Unmarshal(diff, &a.Diff) != nil {
		a.Diff = []HumanGateAdjustmentDiffEntry{}
	}
	cohort, x := s.GetHumanGateCohort(ctx, orgID, toVersionID, time.Now().UTC())
	if x != nil {
		return nil, x
	}
	cohort.OperationReceipt = a.Receipt
	return &HumanGateAdjustResult{ContractVersion: HumanGateContractV1, Cohort: cohort, Adjustment: a}, nil
}
