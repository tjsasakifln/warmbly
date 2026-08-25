package confenge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/warmbly/warmbly/internal/errx"
)

// HumanGateRecomposeInput re-runs the editorial composer over an immutable
// version. It carries no copy: everything it may change is derived, and the
// only thing the operator supplies is the authority to fork the version.
type HumanGateRecomposeInput struct {
	Reason             string `json:"reason"`
	Confirmation       string `json:"confirmation"`
	ExpectedFrozenHash string `json:"expected_frozen_hash"`

	// RawBody is the exact request body, kept for the same reason adjust keeps
	// it: a typed struct silently drops "mailbox", and silently dropping it is
	// how a caller comes to believe it changed the recipient.
	RawBody        []byte `json:"-"`
	IdempotencyKey string `json:"-"`
	CorrelationID  string `json:"-"`
}

// HumanGateRecomposeResult is the 201 body: the whole new version plus the
// report explaining what the composer kept, dropped and rewrote.
type HumanGateRecomposeResult struct {
	ContractVersion        string           `json:"contract_version"`
	Cohort                 *HumanGateCohort `json:"cohort"`
	Report                 RecomposeReport  `json:"report"`
	RevokedAuthorizationID *uuid.UUID       `json:"revoked_authorization_id"`
	Receipt                string           `json:"receipt"`
}

// RecomposeHumanGateCohort creates version N+1 of the same cohort by re-running
// the composer over the parent manifest.
//
// It never updates the parent row. Copy may be rewritten and members may be
// dropped by exclusion, but every recipient-identifying and provenance-
// identifying fact must survive byte-identical: anything else is drift and the
// whole operation is refused. The new version is born with no validation, no
// review and no GO, and queues, dispatches, sends and resumes nothing.
func (s *service) RecomposeHumanGateCohort(ctx context.Context, orgID, actorID, versionID uuid.UUID, in HumanGateRecomposeInput) (*HumanGateRecomposeResult, *errx.Error) {
	if s.humanGateDB == nil {
		return nil, humanGateError(errx.ServiceUnavailable, "human_gate_store_unavailable", "human gate store is not configured")
	}
	if actorID == uuid.Nil {
		return nil, errx.ErrUnauthorized
	}
	// The immutable-field refusal runs before any other validation: recompose
	// derives copy, it never accepts a retyped recipient or evidence.
	if field := humanGateImmutableField(in.RawBody); field != "" {
		return nil, humanGateImmutableFieldError(field)
	}

	reason := strings.TrimSpace(in.Reason)
	confirmation := strings.ToLower(strings.TrimSpace(in.Confirmation))
	expected := strings.TrimSpace(in.ExpectedFrozenHash)
	key := strings.TrimSpace(in.IdempotencyKey)

	if key == "" {
		return nil, humanGateError(errx.BadRequest, "idempotency_key_required", "Idempotency-Key is required")
	}
	if len([]rune(reason)) < HumanGateAdjustMinReason {
		return nil, humanGateError(errx.BadRequest, "recompose_reason_required", fmt.Sprintf("reason is required and must be at least %d characters", HumanGateAdjustMinReason))
	}
	if confirmation == "" {
		return nil, humanGateError(errx.BadRequest, "recompose_confirmation_required", "confirmation must name the immutable version being recomposed, for example \"v1\"")
	}
	if expected == "" {
		return nil, humanGateError(errx.BadRequest, "recompose_expected_frozen_hash_required", "expected_frozen_hash is required")
	}

	unlock, lockErr := s.lockHumanGateIntent(ctx, orgID, key)
	if lockErr != nil {
		return nil, lockErr
	}
	defer unlock()

	reqHash := humanGateRequestHash(struct {
		V          uuid.UUID
		R, F, Cf   string
		Derivation string
	}{versionID, reason, expected, confirmation, DerivationRecompose})

	now := time.Now().UTC()
	parent, x := s.GetHumanGateCohort(ctx, orgID, versionID, now)
	if x != nil {
		return nil, x
	}

	if replay, rx := s.humanGateRecomposeByIdempotency(ctx, orgID, key, reqHash, parent); replay != nil || rx != nil {
		return replay, rx
	}

	if expected != parent.FrozenHash {
		return nil, humanGateError(errx.Conflict, "frozen_hash_mismatch", "expected_frozen_hash does not match the frozen hash of this version; re-read the version before recomposing it")
	}
	if !humanGateVersionConfirmed(confirmation, parent.Version) {
		return nil, humanGateError(errx.Conflict, "confirmation_mismatch", fmt.Sprintf("confirmation must be exactly \"v%d\"", parent.Version))
	}
	if parent.Freshness != "FRESH" {
		return nil, humanGateError(errx.Conflict, "source_stale", "canonical source evidence for this version is stale; recompose rewrites copy, it cannot refresh evidence")
	}
	// No composer_drift guard here on purpose: moving a version onto the current
	// composer is exactly what this operation is for.
	if n := len(parent.Manifest.Members); n < 1 || n > HumanGateMaxCohort {
		return nil, humanGateError(errx.Unprocessable, "cohort_bounds_violation", fmt.Sprintf("cohort holds %d members; the bounded cohort is 1..%d", n, HumanGateMaxCohort))
	}
	if parent.Manifest.MaxDailyVolume < 1 || parent.Manifest.MaxDailyVolume > DefaultCohortDispatchCap {
		return nil, humanGateError(errx.Unprocessable, "cohort_bounds_violation", fmt.Sprintf("max_daily_volume %d is outside the bounded cohort cap 1..%d", parent.Manifest.MaxDailyVolume, DefaultCohortDispatchCap))
	}

	// Deep-copy first: the composer must never be handed the parent's own slices.
	source, err := humanGateCloneManifest(&parent.Manifest)
	if err != nil {
		return nil, humanGateError(errx.Internal, "cohort_read_failed", "cohort version could not be read")
	}
	s.recoverMemberServiceClassification(ctx, orgID, source)
	next, report, err := RecomposeManifest(source)
	if err != nil {
		return nil, humanGateError(errx.Unprocessable, "recompose_failed", err.Error())
	}
	if next == nil {
		return nil, humanGateError(errx.Internal, "recompose_failed", "recomposition produced no manifest")
	}
	if len(next.Members) == 0 {
		return nil, humanGateError(errx.Unprocessable, "recompose_empty_cohort", "recomposition excluded every member; there is nothing left to authorize")
	}
	if drift := humanGateRecomposeDrift(&parent.Manifest, next, report); drift != "" {
		return nil, humanGateError(errx.Conflict, "recompose_drift", "recomposition changed a fact it may not change ("+drift+"); the version is refused instead of stored")
	}

	// The manifest has to say why a member is missing, not just be shorter than
	// its parent. The report alone is not durable evidence.
	next.Exclusions = humanGateMergeExclusions(next.Exclusions, report.Exclusions)

	// Every derived value is recomputed with the freeze path's own helpers, which
	// bind the current composer version. A second hashing implementation would be
	// a second source of truth.
	mailboxes := make([]string, 0, len(next.Members))
	for i := range next.Members {
		m := &next.Members[i]
		m.ContentHash = hashControlledContent(m.Mailbox, m.RouteClass, m.Subject, m.BodyText)
		mailboxes = append(mailboxes, m.Mailbox)
	}
	next.RecipientSetHash = HashRecipientSet(mailboxes)
	next.Preview.SamplesByClass = selectPreviewSamples(next.Members, previewSamplePerClass)
	// N+1 is born with no authority. Carrying the parent's authorization id into
	// a manifest nobody authorized is the two-live-authority bug this operation
	// exists to avoid.
	next.AuthorizationID = uuid.Nil
	next.RealEmailSent = false
	next.AutoSendEnabled = false
	next.GreenAutorunEnabled = false
	next.Warnings = humanGateAppendWarning(next.Warnings, fmt.Sprintf("recomposed_copy:%d", parent.Version))
	next.CohortHash = HashFrozenCohort(next)
	next.CohortID = deriveCohortID(next.SnapshotHash, next.FeedIdentity, next.CohortHash)

	newVersionID, toVersion, revoked, x := s.humanGateCommitRecompose(ctx, orgID, actorID, versionID, parent, next, reason, strings.TrimSpace(in.CorrelationID), key, reqHash)
	if x != nil {
		if replay, rx := s.humanGateRecomposeByIdempotency(ctx, orgID, key, reqHash, parent); replay != nil || rx != nil {
			return replay, rx
		}
		return nil, x
	}

	s.auditHumanGate(ctx, orgID, actorID, "cohort_version_recomposed", newVersionID,
		map[string]string{"version": fmt.Sprint(parent.Version), "frozen_hash": parent.FrozenHash, "composer_version": parent.Manifest.ComposerVersion},
		map[string]string{"version": fmt.Sprint(toVersion), "frozen_hash": next.CohortHash, "composer_version": next.ComposerVersion, "derivation": DerivationRecompose})

	cohort, x := s.GetHumanGateCohort(ctx, orgID, newVersionID, time.Now().UTC())
	if x != nil {
		return nil, x
	}
	receipt := humanGateReceipt("recomposition", newVersionID)
	cohort.OperationReceipt = receipt
	return &HumanGateRecomposeResult{ContractVersion: HumanGateContractV1, Cohort: cohort, Report: report, RevokedAuthorizationID: revoked, Receipt: receipt}, nil
}

// humanGateRecomposeDrift fails closed: it names the first fact the composer
// changed that it may not change. Copy is the only mutable thing, plus members
// disappearing through exclusion.
func humanGateRecomposeDrift(parent, next *FrozenCohortSnapshot, report RecomposeReport) string {
	if next.SnapshotHash != parent.SnapshotHash {
		return "snapshot_hash"
	}
	if next.FeedIdentity != parent.FeedIdentity {
		return "feed_identity"
	}
	if next.FeedSchemaVersion != parent.FeedSchemaVersion {
		return "feed_schema_version"
	}
	if next.AuthoritativeFreshnessHash != parent.AuthoritativeFreshnessHash {
		return "authoritative_freshness_hash"
	}
	if next.AuthoritativeFreshnessRequired != parent.AuthoritativeFreshnessRequired {
		return "authoritative_freshness_required"
	}
	if humanGateFreshnessBytes(parent.AuthoritativeSourceFreshness) != humanGateFreshnessBytes(next.AuthoritativeSourceFreshness) {
		return "authoritative_source_freshness"
	}
	if len(next.Members) > len(parent.Members) {
		return "member_added"
	}
	// The report has to describe the manifest it came with, or the receipt is
	// evidence about something else.
	if report.ParentMembers != len(parent.Members) || report.KeptMembers != len(next.Members) {
		return "report_does_not_reconcile"
	}

	prior := make(map[uuid.UUID]*FrozenCohortMember, len(parent.Members))
	for i := range parent.Members {
		prior[parent.Members[i].CandidateID] = &parent.Members[i]
	}
	seen := make(map[uuid.UUID]bool, len(next.Members))
	for i := range next.Members {
		m := &next.Members[i]
		if seen[m.CandidateID] {
			return "member_duplicated:" + m.CandidateID.String()
		}
		seen[m.CandidateID] = true
		was, ok := prior[m.CandidateID]
		if !ok {
			return "member_not_in_parent:" + m.CandidateID.String()
		}
		switch {
		case m.Mailbox != was.Mailbox:
			return "mailbox:" + m.CandidateID.String()
		case m.AccountRef != was.AccountRef:
			return "account_ref:" + m.CandidateID.String()
		case m.CandidateRef != was.CandidateRef:
			return "candidate_ref:" + m.CandidateID.String()
		case m.AccountID != was.AccountID:
			return "account_id:" + m.CandidateID.String()
		case m.RouteClass != was.RouteClass:
			return "route_class:" + m.CandidateID.String()
		case m.EvidenceHash != was.EvidenceHash:
			return "evidence_hash:" + m.CandidateID.String()
		case m.Source != was.Source:
			return "source:" + m.CandidateID.String()
		}
	}
	return ""
}

func humanGateMergeExclusions(existing, added []CohortExclusion) []CohortExclusion {
	seen := make(map[CohortExclusion]bool, len(existing))
	out := make([]CohortExclusion, 0, len(existing)+len(added))
	for _, e := range existing {
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	for _, e := range added {
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	return out
}

func humanGateFreshnessBytes(f *FeedSourceFreshness) string {
	if f == nil {
		return ""
	}
	b, err := json.Marshal(f)
	if err != nil {
		return "\x00unmarshalable"
	}
	return string(b)
}

// humanGateCommitRecompose is the whole side effect, in one transaction: lock
// the parent, prove it is still the latest version, revoke any live authority it
// carries, insert N+1. Nothing here updates the parent's frozen_manifest, and no
// validation, review or GO row is copied forward.
func (s *service) humanGateCommitRecompose(ctx context.Context, orgID, actorID, versionID uuid.UUID, parent *HumanGateCohort, next *FrozenCohortSnapshot, reason, correlationID, key, reqHash string) (uuid.UUID, int, *uuid.UUID, *errx.Error) {
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
		return uuid.Nil, 0, nil, humanGateError(errx.Conflict, "frozen_hash_mismatch", "this version changed while the recomposition was being prepared; re-read it")
	}

	var maxVersion int
	if err = tx.QueryRow(ctx, `SELECT COALESCE(max(version),0) FROM confenge_cohort_versions WHERE organization_id=$1 AND cohort_id=$2`, orgID, parent.CohortID).Scan(&maxVersion); err != nil {
		return uuid.Nil, 0, nil, humanGateError(errx.ServiceUnavailable, "human_gate_read_failed", "cohort versions could not be read")
	}
	if maxVersion != lockedVersion {
		return uuid.Nil, 0, nil, humanGateError(errx.Conflict, "version_superseded", fmt.Sprintf("version %d is no longer the latest version of this cohort (latest is %d); re-read it before recomposing", lockedVersion, maxVersion))
	}

	revoked, x := humanGateRevokePriorAuthority(ctx, tx, orgID, actorID, versionID, "human_gate_recompose:"+reason)
	if x != nil {
		return uuid.Nil, 0, nil, x
	}

	toVersion := lockedVersion + 1
	manifest, err := json.Marshal(next)
	if err != nil {
		return uuid.Nil, 0, nil, humanGateError(errx.Internal, "cohort_store_failed", "recomposed manifest could not be encoded")
	}
	selectionReport, err := json.Marshal(parent.Selection)
	if err != nil {
		return uuid.Nil, 0, nil, humanGateError(errx.Internal, "cohort_store_failed", "selection report could not be encoded")
	}
	newVersionID := uuid.New()
	_, err = tx.Exec(ctx, `INSERT INTO confenge_cohort_versions
		(id,organization_id,cohort_id,version,source_run_id,source_system,source_as_of,freshness_expires_at,policy_version,frozen_hash,frozen_manifest,derivation,parent_version,selection_mode,selection_report,created_by,correlation_id,idempotency_key,request_hash)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
		newVersionID, orgID, parent.CohortID, toVersion, parent.SourceRunID, parent.Source, parent.AsOf, parent.FreshUntil,
		parent.PolicyVersion, next.CohortHash, manifest, DerivationRecompose, lockedVersion, parent.Selection.Mode, selectionReport,
		actorID, correlationID, key, reqHash)
	if err != nil {
		return uuid.Nil, 0, nil, humanGateAdjustWriteError(err, "cohort_store_failed", "recomposed cohort version could not be stored")
	}

	if err = tx.Commit(ctx); err != nil {
		return uuid.Nil, 0, nil, humanGateAdjustWriteError(err, "cohort_store_failed", "recomposition could not be committed")
	}
	return newVersionID, toVersion, revoked, nil
}

// humanGateRecomposeByIdempotency replays a stored recomposition. A replay must
// return the same version and must never create a second one. The report is
// re-derived from the two stored manifests, because a recomposition receipt has
// no table of its own: the manifests are the evidence.
func (s *service) humanGateRecomposeByIdempotency(ctx context.Context, orgID uuid.UUID, key, reqHash string, parent *HumanGateCohort) (*HumanGateRecomposeResult, *errx.Error) {
	var id uuid.UUID
	var stored, derivation string
	err := s.humanGateDB.QueryRow(ctx, `SELECT id,request_hash,derivation FROM confenge_cohort_versions WHERE organization_id=$1 AND idempotency_key=$2`, orgID, key).Scan(&id, &stored, &derivation)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, humanGateError(errx.Internal, "idempotency_read_failed", "idempotency receipt could not be read")
	}
	if stored != reqHash || derivation != DerivationRecompose {
		return nil, humanGateError(errx.Conflict, "idempotency_payload_conflict", "Idempotency-Key was already used with another payload")
	}
	cohort, x := s.GetHumanGateCohort(ctx, orgID, id, time.Now().UTC())
	if x != nil {
		return nil, x
	}
	receipt := humanGateReceipt("recomposition", id)
	cohort.OperationReceipt = receipt
	return &HumanGateRecomposeResult{
		ContractVersion: HumanGateContractV1,
		Cohort:          cohort,
		Report:          humanGateRecomposeReplayReport(&parent.Manifest, &cohort.Manifest),
		Receipt:         receipt,
	}, nil
}

// humanGateRecomposeReplayReport reconstructs what the composer did from the
// parent and stored manifests. Exclusions the parent already carried are not
// this recomposition's work.
func humanGateRecomposeReplayReport(parent, stored *FrozenCohortSnapshot) RecomposeReport {
	report := RecomposeReport{
		ParentMembers:   len(parent.Members),
		KeptMembers:     len(stored.Members),
		ExcludedMembers: len(parent.Members) - len(stored.Members),
		Exclusions:      []CohortExclusion{},
		ByReasonCode:    map[string]int{},
		ComposerBefore:  parent.ComposerVersion,
		ComposerAfter:   stored.ComposerVersion,
	}
	prior := map[CohortExclusion]bool{}
	for _, e := range parent.Exclusions {
		prior[e] = true
	}
	for _, e := range stored.Exclusions {
		if prior[e] {
			continue
		}
		report.Exclusions = append(report.Exclusions, e)
		report.ByReasonCode[e.ReasonCode]++
	}
	return report
}

// recoverMemberServiceClassification fills the service and moment codes on the
// working copy of a manifest frozen before those fields existed. It reads
// CONFENGE's own classification of the account, never the lead's public record,
// so this is not the upstream re-read that would make a recomposition a CREATE.
// The stored parent is untouched: source is already a deep copy.
func (s *service) recoverMemberServiceClassification(ctx context.Context, orgID uuid.UUID, source *FrozenCohortSnapshot) {
	if source == nil || s.repo == nil {
		return
	}
	for i := range source.Members {
		m := &source.Members[i]
		if strings.TrimSpace(m.ServiceCode) != "" || m.AccountID == uuid.Nil {
			continue
		}
		acc, err := s.repo.GetAccount(ctx, orgID, m.AccountID)
		if err != nil || acc == nil {
			continue
		}
		m.ServiceCode = strings.TrimSpace(acc.ServiceCode)
		if strings.TrimSpace(m.MomentCode) == "" {
			m.MomentCode = strings.TrimSpace(acc.MomentCode)
		}
	}
}
