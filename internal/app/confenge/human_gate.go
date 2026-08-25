package confenge

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	verify "github.com/warmbly/warmbly/internal/pkg/emailverify"
)

const (
	HumanGateContractV1    = "confenge.human-gate.v1"
	HumanGateSource        = "warmbly.controlled-outbound"
	HumanGateValidationTTL = 24 * time.Hour
	HumanGateMaxCohort     = 10
	HumanGateReadyVerdict  = "READY_FOR_HUMAN_GATE_LIVE_PREFLIGHT"
)

type HumanGateCreateInput struct {
	Limit             int         `json:"limit"`
	SourceRunID       string      `json:"source_run_id,omitempty"`
	SelectionMode     string      `json:"selection_mode,omitempty"`
	RecoverVersionIDs []uuid.UUID `json:"recover_version_ids,omitempty"`
	IdempotencyKey    string      `json:"-"`
	CorrelationID     string      `json:"-"`
}

const (
	HumanGateSelectionLegacy        = "LEGACY"
	HumanGateSelectionNextUnclaimed = "NEXT_UNCLAIMED"
	HumanGateSelectionRecoverPrior  = "RECOVER_PRIOR"
)

type HumanGateSelection struct {
	Mode               string         `json:"mode"`
	SourceRunID        string         `json:"source_run_id"`
	ClaimedCount       int            `json:"claimed_count"`
	UniqueClaimedTotal int            `json:"unique_claimed_total"`
	EligibleRemaining  int            `json:"eligible_remaining"`
	Exhausted          bool           `json:"exhausted"`
	RecoveredRequested int            `json:"recovered_requested,omitempty"`
	RecoveredEligible  int            `json:"recovered_eligible,omitempty"`
	ExclusionsByReason map[string]int `json:"exclusions_by_reason,omitempty"`
}

type HumanGateValidation struct {
	ID           uuid.UUID `json:"id"`
	Status       string    `json:"status"`
	Reason       string    `json:"reason"`
	Provider     string    `json:"provider"`
	Method       string    `json:"method"`
	EvidenceHash string    `json:"evidence_hash"`
	CheckedAt    time.Time `json:"checked_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	Correlation  string    `json:"correlation_id"`
	Receipt      string    `json:"receipt"`
}

type HumanGateReview struct {
	ID            uuid.UUID `json:"id"`
	Decision      string    `json:"decision"`
	Reason        string    `json:"reason"`
	Effective     bool      `json:"effective"`
	InvalidatedBy []string  `json:"invalidated_by"`
	ActorID       uuid.UUID `json:"actor_id"`
	CreatedAt     time.Time `json:"created_at"`
	Correlation   string    `json:"correlation_id"`
	Receipt       string    `json:"receipt"`
}

type HumanGateCandidate struct {
	FrozenCohortMember
	Validation *HumanGateValidation `json:"validation"`
	Review     *HumanGateReview     `json:"review"`
	Scheduling *HumanGateScheduling `json:"scheduling"`
	BlockedBy  []string             `json:"blocked_by"`
	// Editorial authority projection, so a renderer never has to compare
	// composer strings itself to know whether this text is still operable.
	EditorialState       string   `json:"editorial_state"`
	Actionable           bool     `json:"actionable"`
	EditorialReasonCodes []string `json:"editorial_reason_codes"`
}

// HumanGateScheduling is the server-confirmed effect of an APPROVE. AutoSend
// is per message: it means the approved touchpoint will be picked up by the
// Warmbly worker. It is unrelated to CONFENGE_AUTO_SEND_ENABLED, which remains
// prohibited globally.
type HumanGateScheduling struct {
	TouchpointID       uuid.UUID  `json:"touchpoint_id"`
	DraftID            uuid.UUID  `json:"draft_id"`
	State              string     `json:"state"`
	AutoSend           bool       `json:"auto_send"`
	DueAt              time.Time  `json:"due_at"`
	ScheduledAt        time.Time  `json:"scheduled_at"`
	InvalidatedAt      *time.Time `json:"invalidated_at,omitempty"`
	InvalidationReason string     `json:"invalidation_reason,omitempty"`
}

type HumanGateReconcileFailure struct {
	CohortVersionID uuid.UUID `json:"cohort_version_id"`
	CandidateID     uuid.UUID `json:"candidate_id"`
	Reason          string    `json:"reason"`
}

// HumanGateReconcileReport is safe to run repeatedly. ApprovalRecords counts
// the durable APPROVE history; UniqueApprovedCandidates is the number of
// distinct messages after duplicate approvals converge.
type HumanGateReconcileReport struct {
	ApprovalRecords          int                         `json:"approval_records"`
	LatestApprovedBindings   int                         `json:"latest_approved_bindings"`
	UniqueApprovedCandidates int                         `json:"unique_approved_candidates"`
	Scheduled                int                         `json:"scheduled"`
	AlreadyScheduled         int                         `json:"already_scheduled"`
	Failed                   int                         `json:"failed"`
	Failures                 []HumanGateReconcileFailure `json:"failures"`
}

// HumanGateDecision is a read-only projection of legacy GO/NO-GO records.
// The write endpoint was removed when individual APPROVE became the complete
// scheduling authority. Keeping history visible is audit, not a live gate.
type HumanGateDecision struct {
	ID              uuid.UUID  `json:"id"`
	Decision        string     `json:"decision"`
	Reason          string     `json:"reason"`
	ActorID         uuid.UUID  `json:"actor_id"`
	CreatedAt       time.Time  `json:"created_at"`
	Correlation     string     `json:"correlation_id"`
	Receipt         string     `json:"receipt"`
	AuthorizationID *uuid.UUID `json:"authorization_id,omitempty"`
	QueueState      string     `json:"queue_state"`
}

type HumanGateCohort struct {
	ContractVersion string    `json:"contract_version"`
	ID              uuid.UUID `json:"id"`
	CohortID        uuid.UUID `json:"cohort_id"`
	Version         int       `json:"version"`
	// Derivation says how this version came to exist: CREATE, REPRODUCE,
	// RECOMPOSE (reserved) or ADJUST. ParentVersion is the version it was
	// derived from, nil only for CREATE.
	Derivation       string               `json:"derivation"`
	ParentVersion    *int                 `json:"parent_version"`
	Source           string               `json:"source"`
	SourceRunID      string               `json:"source_run_id"`
	AsOf             time.Time            `json:"as_of"`
	Freshness        string               `json:"freshness"`
	FreshUntil       time.Time            `json:"fresh_until"`
	PolicyVersion    string               `json:"policy_version"`
	FrozenHash       string               `json:"frozen_hash"`
	Manifest         FrozenCohortSnapshot `json:"manifest"`
	Selection        HumanGateSelection   `json:"selection"`
	Candidates       []HumanGateCandidate `json:"candidates"`
	Decision         *HumanGateDecision   `json:"decision"`
	Reason           []string             `json:"reason"`
	CorrelationID    string               `json:"correlation_id"`
	Receipt          string               `json:"receipt"`
	OperationReceipt string               `json:"-"`
	CreatedAt        time.Time            `json:"created_at"`
	// Editorial authority projection. The founder must be able to tell a
	// current version from history without reading a version number, and must
	// be handed the current version to open instead.
	EditorialState       string     `json:"editorial_state"`
	Actionable           bool       `json:"actionable"`
	EditorialReasonCodes []string   `json:"editorial_reason_codes"`
	EditorialNotice      string     `json:"editorial_notice,omitempty"`
	IsCurrentVersion     bool       `json:"is_current_version"`
	CurrentVersion       int        `json:"current_version"`
	CurrentVersionID     *uuid.UUID `json:"current_version_id,omitempty"`
}

type HumanGateReviewInput struct {
	Decision       string `json:"decision"`
	Reason         string `json:"reason"`
	Acknowledged   bool   `json:"acknowledged,omitempty"`
	IdempotencyKey string `json:"-"`
	CorrelationID  string `json:"-"`
}

func (s *service) WireHumanGate(db *pgxpool.Pool) { s.humanGateDB = db }

func humanGateRequestHash(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func humanGateReceipt(kind string, id uuid.UUID) string { return kind + ":" + id.String() }

func humanGateVersionConfirmed(confirmation string, version int) bool {
	return strings.ToLower(strings.TrimSpace(confirmation)) == fmt.Sprintf("v%d", version)
}

func humanGateError(code errx.Code, id, message string) *errx.Error {
	return errx.NewWithIdentifier(code, id, message)
}

// Session advisory locking closes the gap between "read idempotency receipt"
// and side effects such as creating a bounded transport authority. A UNIQUE
// constraint protects rows, but by itself cannot prevent two concurrent GO
// calls from creating two authorities before one row loses the race.
func (s *service) lockHumanGateIntent(ctx context.Context, orgID uuid.UUID, key string) (func(), *errx.Error) {
	if s.humanGateDB == nil {
		return nil, humanGateError(errx.ServiceUnavailable, "human_gate_store_unavailable", "human gate store is not configured")
	}
	sum := sha256.Sum256([]byte(orgID.String() + "\x00" + strings.TrimSpace(key)))
	lockID := int64(binary.BigEndian.Uint64(sum[:8]))
	conn, err := s.humanGateDB.Acquire(ctx)
	if err != nil {
		return nil, humanGateError(errx.ServiceUnavailable, "idempotency_lock_unavailable", "human gate idempotency lock is unavailable")
	}
	if _, err = conn.Exec(ctx, "SELECT pg_advisory_lock($1)", lockID); err != nil {
		conn.Release()
		return nil, humanGateError(errx.ServiceUnavailable, "idempotency_lock_unavailable", "human gate idempotency lock is unavailable")
	}
	return func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", lockID)
		conn.Release()
	}, nil
}

func (s *service) CreateHumanGateCohort(ctx context.Context, orgID, actorID uuid.UUID, in HumanGateCreateInput) (*HumanGateCohort, *errx.Error) {
	if x := s.requireEnabled(); x != nil {
		return nil, x
	}
	if s.humanGateDB == nil {
		return nil, humanGateError(errx.ServiceUnavailable, "human_gate_store_unavailable", "human gate store is not configured")
	}
	if actorID == uuid.Nil {
		return nil, errx.ErrUnauthorized
	}
	if strings.TrimSpace(in.IdempotencyKey) == "" {
		return nil, humanGateError(errx.BadRequest, "idempotency_key_required", "Idempotency-Key is required")
	}
	unlock, lockErr := s.lockHumanGateIntent(ctx, orgID, in.IdempotencyKey)
	if lockErr != nil {
		return nil, lockErr
	}
	defer unlock()
	if in.Limit == 0 {
		in.Limit = 5
	}
	if in.Limit < 1 || in.Limit > HumanGateMaxCohort {
		return nil, humanGateError(errx.BadRequest, "invalid_cohort_limit", "limit must be between 1 and 10")
	}
	if x := normalizeHumanGateSelection(&in); x != nil {
		return nil, x
	}
	reqHash := humanGateSelectionRequestHash(in)
	if existing, x := s.humanGateByIdempotency(ctx, orgID, in.IdempotencyKey, reqHash); existing != nil || x != nil {
		return existing, x
	}

	runs, err := s.repo.ListImportRuns(ctx, orgID, 20)
	if err != nil {
		return nil, humanGateError(errx.ServiceUnavailable, "source_unavailable", "canonical source could not be read")
	}
	var run *models.OutreachImportRun
	for i := range runs {
		if runs[i].Status == models.OutreachImportCompleted && (in.SourceRunID == "" || runs[i].SourceRunID == in.SourceRunID) {
			run = &runs[i]
			break
		}
	}
	if run == nil {
		return nil, humanGateError(errx.Unprocessable, "source_run_not_found", "no completed canonical source run matches the request")
	}
	asOf := run.CreatedAt.UTC()
	if run.SourceGeneratedAt != nil {
		asOf = run.SourceGeneratedAt.UTC()
	} else if run.FinishedAt != nil {
		asOf = run.FinishedAt.UTC()
	}
	maxAge := s.cfg.FeedMaxAge
	if maxAge <= 0 {
		maxAge = 24 * time.Hour
	}
	now := time.Now().UTC()
	if !now.Before(asOf.Add(maxAge)) {
		return nil, humanGateError(errx.Conflict, "source_stale", "canonical source evidence is stale")
	}
	freshness := &FeedSourceFreshness{ContractVersion: AuthoritativeFreshnessContractV1, Status: "FRESH", AsOf: asOf.Format(time.RFC3339Nano), ExpiresAt: asOf.Add(maxAge).Format(time.RFC3339Nano), DeployedSHA: run.RepoSHA, PolicyVersion: run.ProfileVersion, RunID: run.SourceRunID}
	prepareOpts := CohortPrepareOptions{
		Now: now, Limit: in.Limit, MaxDailyVolume: in.Limit, TTL: DefaultCohortTTL,
		RepositorySHA: s.cfg.RepositorySHA, FeedSchemaVersion: firstNonEmpty(s.cfg.FeedSchemaVersion, run.SchemaVersion),
		FeedIdentity: run.SourceRunID, SnapshotHash: run.SnapshotHash, PolicyVersion: BoundedCohortPolicyV1,
		EvidenceVersion: firstNonEmpty(s.cfg.EvidenceVersion, DefaultEvidenceVersion), Source: run.SourceSystem,
		AuthoritativeSourceFreshness: freshness, RequireAuthoritativeFreshness: true,
	}
	selection := HumanGateSelection{Mode: in.SelectionMode, SourceRunID: run.SourceRunID}
	var manifest *FrozenCohortSnapshot
	var selectedAccounts map[uuid.UUID]models.OutreachAccount
	var tx pgx.Tx
	if in.SelectionMode == HumanGateSelectionLegacy {
		accounts, readErr := AccountsFromOrgForRun(ctx, s.repo, orgID, run.SourceSystem, run.SourceRunID)
		if readErr != nil {
			return nil, humanGateError(errx.Unprocessable, "source_accounts_unavailable", readErr.Error())
		}
		manifest, err = PrepareControlledCohort(accounts, prepareOpts)
		if err != nil {
			return nil, humanGateError(errx.Unprocessable, "cohort_prepare_failed", err.Error())
		}
		selection.ClaimedCount = len(manifest.Members)
	} else {
		tx, err = s.humanGateDB.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return nil, humanGateError(errx.ServiceUnavailable, "human_gate_store_unavailable", "human gate transaction could not be started")
		}
		defer func() { _ = tx.Rollback(ctx) }()
		var x *errx.Error
		manifest, selection, selectedAccounts, x = s.prepareHumanGateSelection(ctx, tx, orgID, run, in, prepareOpts)
		if x != nil {
			return nil, x
		}
	}
	cohortID, versionID := uuid.New(), uuid.New()
	b, _ := json.Marshal(manifest)
	selectionJSON, _ := json.Marshal(selection)
	store := interface {
		Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	}(s.humanGateDB)
	if tx != nil {
		store = tx
	}
	_, err = store.Exec(ctx, `INSERT INTO confenge_cohort_versions
		(id,organization_id,cohort_id,version,source_run_id,source_system,source_as_of,freshness_expires_at,policy_version,frozen_hash,frozen_manifest,selection_mode,selection_report,created_by,correlation_id,idempotency_key,request_hash)
		VALUES($1,$2,$3,1,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`, versionID, orgID, cohortID,
		run.SourceRunID, run.SourceSystem, asOf, asOf.Add(maxAge), manifest.PolicyVersion, manifest.CohortHash, b, selection.Mode, selectionJSON, actorID,
		strings.TrimSpace(in.CorrelationID), strings.TrimSpace(in.IdempotencyKey), reqHash)
	if err != nil {
		if existing, x := s.humanGateByIdempotency(ctx, orgID, in.IdempotencyKey, reqHash); existing != nil || x != nil {
			return existing, x
		}
		return nil, humanGateError(errx.Internal, "cohort_store_failed", "cohort version could not be stored")
	}
	if tx != nil {
		if x := storeHumanGateSelectionClaims(ctx, tx, orgID, versionID, run.SourceRunID, in.SelectionMode, manifest, selectedAccounts); x != nil {
			return nil, x
		}
		if x := finalizeHumanGateSelectionReport(ctx, tx, orgID, run.SourceRunID, &selection); x != nil {
			return nil, x
		}
		selectionJSON, _ = json.Marshal(selection)
		if _, err = tx.Exec(ctx, `UPDATE confenge_cohort_versions SET selection_report=$1 WHERE id=$2 AND organization_id=$3`, selectionJSON, versionID, orgID); err != nil {
			return nil, humanGateError(errx.Internal, "cohort_store_failed", "cohort selection report could not be stored")
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, humanGateError(errx.Internal, "cohort_store_failed", "cohort selection could not be committed")
		}
	}
	s.auditHumanGate(ctx, orgID, actorID, "cohort_version_created", versionID, map[string]string{"state": "absent"}, map[string]string{"state": "FROZEN", "frozen_hash": manifest.CohortHash})
	return s.GetHumanGateCohort(ctx, orgID, versionID, now)
}

func (s *service) ReproduceHumanGateCohort(ctx context.Context, orgID, actorID, id uuid.UUID, in HumanGateCreateInput) (*HumanGateCohort, *errx.Error) {
	if actorID == uuid.Nil {
		return nil, errx.ErrUnauthorized
	}
	if strings.TrimSpace(in.IdempotencyKey) == "" {
		return nil, humanGateError(errx.BadRequest, "idempotency_key_required", "Idempotency-Key is required")
	}
	unlock, lockErr := s.lockHumanGateIntent(ctx, orgID, in.IdempotencyKey)
	if lockErr != nil {
		return nil, lockErr
	}
	defer unlock()
	old, x := s.GetHumanGateCohort(ctx, orgID, id, time.Now().UTC())
	if x != nil {
		return nil, x
	}
	reqHash := humanGateRequestHash(struct{ ID uuid.UUID }{id})
	if existing, x := s.humanGateByIdempotency(ctx, orgID, in.IdempotencyKey, reqHash); existing != nil || x != nil {
		return existing, x
	}
	newID := uuid.New()
	b, _ := json.Marshal(old.Manifest)
	var version int
	err := s.humanGateDB.QueryRow(ctx, `INSERT INTO confenge_cohort_versions
		(id,organization_id,cohort_id,version,source_run_id,source_system,source_as_of,freshness_expires_at,policy_version,frozen_hash,frozen_manifest,reproduced_from_version,derivation,parent_version,selection_mode,selection_report,created_by,correlation_id,idempotency_key,request_hash)
		SELECT $1,organization_id,cohort_id,(SELECT max(version)+1 FROM confenge_cohort_versions WHERE organization_id=$2 AND cohort_id=$3),source_run_id,source_system,source_as_of,freshness_expires_at,policy_version,frozen_hash,$4,version,'REPRODUCE',version,selection_mode,selection_report,$5,$6,$7,$8
		FROM confenge_cohort_versions WHERE id=$9 AND organization_id=$2 RETURNING version`, newID, orgID, old.CohortID, b, actorID, in.CorrelationID, in.IdempotencyKey, reqHash, id).Scan(&version)
	if err != nil {
		if existing, idemErr := s.humanGateByIdempotency(ctx, orgID, in.IdempotencyKey, reqHash); existing != nil || idemErr != nil {
			return existing, idemErr
		}
		return nil, humanGateError(errx.Conflict, "cohort_reproduce_conflict", "cohort reproduction conflicted; read the latest version")
	}
	s.auditHumanGate(ctx, orgID, actorID, "cohort_version_reproduced", newID, map[string]string{"version": fmt.Sprint(old.Version)}, map[string]string{"version": fmt.Sprint(version), "frozen_hash": old.FrozenHash})
	return s.GetHumanGateCohort(ctx, orgID, newID, time.Now().UTC())
}

func (s *service) ListHumanGateCohorts(ctx context.Context, orgID uuid.UUID, limit int, cursor time.Time, now time.Time) ([]HumanGateCohort, *errx.Error) {
	if s.humanGateDB == nil {
		return nil, humanGateError(errx.ServiceUnavailable, "human_gate_store_unavailable", "human gate store is not configured")
	}
	if limit < 1 || limit > 100 {
		limit = 25
	}
	if cursor.IsZero() {
		cursor = time.Now().UTC().AddDate(100, 0, 0)
	}
	rows, err := s.humanGateDB.Query(ctx, `SELECT id FROM confenge_cohort_versions WHERE organization_id=$1 AND created_at < $2 ORDER BY created_at DESC,id DESC LIMIT $3`, orgID, cursor, limit)
	if err != nil {
		return nil, humanGateError(errx.ServiceUnavailable, "human_gate_read_failed", "cohorts could not be read")
	}
	defer rows.Close()
	out := make([]HumanGateCohort, 0, limit)
	for rows.Next() {
		var id uuid.UUID
		if rows.Scan(&id) == nil {
			if v, x := s.GetHumanGateCohort(ctx, orgID, id, now); x == nil {
				out = append(out, *v)
			}
		}
	}
	return out, nil
}

func (s *service) GetHumanGateCohort(ctx context.Context, orgID, id uuid.UUID, now time.Time) (*HumanGateCohort, *errx.Error) {
	if s.humanGateDB == nil {
		return nil, humanGateError(errx.ServiceUnavailable, "human_gate_store_unavailable", "human gate store is not configured")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	v := &HumanGateCohort{ContractVersion: HumanGateContractV1, ID: id, Source: HumanGateSource, CorrelationID: uuid.NewString(), Receipt: humanGateReceipt("cohort", id)}
	var raw, selectionRaw []byte
	var selectionMode string
	err := s.humanGateDB.QueryRow(ctx, `SELECT cohort_id,version,derivation,parent_version,source_run_id,source_system,source_as_of,freshness_expires_at,policy_version,frozen_hash,frozen_manifest,selection_mode,selection_report,correlation_id,created_at FROM confenge_cohort_versions WHERE id=$1 AND organization_id=$2`, id, orgID).Scan(&v.CohortID, &v.Version, &v.Derivation, &v.ParentVersion, &v.SourceRunID, &v.Source, &v.AsOf, &v.FreshUntil, &v.PolicyVersion, &v.FrozenHash, &raw, &selectionMode, &selectionRaw, &v.CorrelationID, &v.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, humanGateError(errx.NotFound, "cohort_version_not_found", "cohort version was not found")
	}
	if err != nil || json.Unmarshal(raw, &v.Manifest) != nil {
		return nil, humanGateError(errx.Internal, "cohort_read_failed", "cohort version could not be read")
	}
	v.Selection = HumanGateSelection{Mode: selectionMode, SourceRunID: v.SourceRunID}
	if len(selectionRaw) > 0 && string(selectionRaw) != "{}" {
		if err = json.Unmarshal(selectionRaw, &v.Selection); err != nil {
			return nil, humanGateError(errx.Internal, "cohort_read_failed", "cohort selection report could not be read")
		}
	}
	if now.Before(v.FreshUntil) {
		v.Freshness = "FRESH"
	} else {
		v.Freshness = "STALE"
		v.Reason = append(v.Reason, "source_evidence_stale")
	}
	policyCurrent := v.PolicyVersion == BoundedCohortPolicyV1
	if !policyCurrent {
		v.Reason = append(v.Reason, "policy_version_stale")
	}
	v.Candidates = make([]HumanGateCandidate, 0, len(v.Manifest.Members))
	current := map[uuid.UUID]CohortAccountInput{}
	if accounts, liveErr := AccountsFromOrgForRun(ctx, s.repo, orgID, v.Source, v.SourceRunID); liveErr == nil {
		for _, account := range accounts {
			current[account.Account.ID] = account
		}
	} else {
		v.Reason = append(v.Reason, "live_suppression_state_unknown")
	}
	for _, m := range v.Manifest.Members {
		c := HumanGateCandidate{FrozenCohortMember: m, BlockedBy: []string{}}
		c.Validation = s.latestHumanGateValidation(ctx, orgID, id, m.CandidateID, now)
		c.Review = s.latestHumanGateReview(ctx, orgID, id, m, BoundedCohortPolicyV1, c.Validation, now)
		c.Scheduling = s.humanGateScheduling(ctx, orgID, id, m.CandidateID)
		if c.Validation == nil {
			c.BlockedBy = append(c.BlockedBy, "validation_missing")
		} else if c.Validation.Status != "VALID" {
			c.BlockedBy = append(c.BlockedBy, "validation_"+strings.ToLower(c.Validation.Status))
		}
		if c.Review == nil || !c.Review.Effective {
			c.BlockedBy = append(c.BlockedBy, "approval_missing_or_invalid")
		}
		if !policyCurrent {
			c.BlockedBy = append(c.BlockedBy, "policy_drift")
		}
		live, ok := current[m.AccountID]
		liveReasons := humanGateLiveInvalidations(m, live, ok)
		if len(liveReasons) > 0 {
			c.BlockedBy = append(c.BlockedBy, liveReasons...)
			if c.Review != nil {
				c.Review.Effective = false
				c.Review.InvalidatedBy = append(c.Review.InvalidatedBy, liveReasons...)
			}
		}
		ca := EvaluateCohortEditorialAuthority(m.ComposerVersion, v.PolicyVersion)
		c.EditorialState, c.Actionable, c.EditorialReasonCodes = ca.State, ca.Actionable, ca.ReasonCodes
		if !ca.Actionable {
			c.BlockedBy = append(c.BlockedBy, ca.ReasonCodes...)
		}
		v.Candidates = append(v.Candidates, c)
	}
	s.projectHumanGateEditorialAuthority(ctx, orgID, v)
	v.Decision = s.latestHumanGateDecision(ctx, orgID, id)
	if v.Decision != nil && v.Decision.Decision == "GO" {
		if blockers := humanGateDecisionBlockers(v); len(blockers) > 0 {
			// GET remains a pure read: it projects that historical GO is no longer
			// queue-ready. The transport authority independently revalidates these
			// same live gates and cannot dispatch through this state.
			v.Decision.QueueState = "BLOCKED_BY_INVALIDATION"
			v.Reason = append(v.Reason, "go_invalidated_by_current_gate_state")
		}
	}
	return v, nil
}

// projectHumanGateEditorialAuthority stamps the version-level verdict and, when
// the version is history, names the version the founder should open instead.
func (s *service) projectHumanGateEditorialAuthority(ctx context.Context, orgID uuid.UUID, v *HumanGateCohort) {
	auth := SnapshotEditorialAuthority(&v.Manifest)
	v.EditorialState, v.Actionable = auth.State, auth.Actionable
	v.EditorialReasonCodes, v.EditorialNotice = auth.ReasonCodes, auth.Notice
	if !auth.Actionable {
		v.Reason = append(v.Reason, auth.ReasonCodes...)
	}
	v.CurrentVersion = v.Version
	v.IsCurrentVersion = true
	var latestID uuid.UUID
	var latest int
	err := s.humanGateDB.QueryRow(ctx,
		`SELECT id,version FROM confenge_cohort_versions WHERE organization_id=$1 AND cohort_id=$2 ORDER BY version DESC LIMIT 1`,
		orgID, v.CohortID).Scan(&latestID, &latest)
	if err != nil {
		return
	}
	v.CurrentVersion = latest
	v.IsCurrentVersion = latest == v.Version
	if !v.IsCurrentVersion {
		id := latestID
		v.CurrentVersionID = &id
		v.Reason = append(v.Reason, "superseded_by_version")
	}
}

func humanGateLiveInvalidations(m FrozenCohortMember, live CohortAccountInput, known bool) []string {
	if !known {
		return []string{"live_candidate_state_unknown"}
	}
	reasons := []string{}
	if live.Account.Blocked {
		reasons = append(reasons, "late_account_suppression")
	}
	if live.Account.DoNotContact {
		reasons = append(reasons, "late_account_opt_out")
	}
	var candidate *models.OutreachContactCandidate
	for i := range live.Candidates {
		if live.Candidates[i].ID == m.CandidateID {
			candidate = &live.Candidates[i]
			break
		}
	}
	if candidate == nil {
		return append(reasons, "candidate_removed")
	}
	if candidate.Blocked {
		reasons = append(reasons, "late_recipient_suppression")
	}
	if candidate.DoNotContact {
		reasons = append(reasons, "late_recipient_opt_out")
	}
	if candidate.Bounced {
		reasons = append(reasons, "late_hard_bounce")
	}
	if !CandidateControlledEligible(candidate) || !ControlledRouteAllowed(candidate, nil) {
		reasons = append(reasons, "controlled_route_no_longer_eligible")
	}
	if canonicalPilotEmail(candidate.Email) != canonicalPilotEmail(m.Mailbox) {
		reasons = append(reasons, "recipient_drift")
	}
	return reasons
}

func (s *service) humanGateByIdempotency(ctx context.Context, orgID uuid.UUID, key, reqHash string) (*HumanGateCohort, *errx.Error) {
	var id uuid.UUID
	var stored string
	err := s.humanGateDB.QueryRow(ctx, `SELECT id,request_hash FROM confenge_cohort_versions WHERE organization_id=$1 AND idempotency_key=$2`, orgID, key).Scan(&id, &stored)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, humanGateError(errx.Internal, "idempotency_read_failed", "idempotency receipt could not be read")
	}
	if stored != reqHash {
		return nil, humanGateError(errx.Conflict, "idempotency_payload_conflict", "Idempotency-Key was already used with another payload")
	}
	return s.GetHumanGateCohort(ctx, orgID, id, time.Now().UTC())
}

func (s *service) humanGateWithOperationReceipt(ctx context.Context, orgID, versionID uuid.UUID, receipt string) (*HumanGateCohort, *errx.Error) {
	v, x := s.GetHumanGateCohort(ctx, orgID, versionID, time.Now().UTC())
	if v != nil {
		v.OperationReceipt = receipt
	}
	return v, x
}

func normalizeValidationStatus(s verify.Status) string {
	switch s {
	case verify.StatusValid:
		return "VALID"
	case verify.StatusRisky:
		return "RISKY"
	case verify.StatusInvalid:
		return "INVALID"
	default:
		return "UNKNOWN"
	}
}

func (s *service) RecordHumanGateValidation(ctx context.Context, orgID, actorID, versionID, candidateID uuid.UUID, result verify.Result, key, correlation string) (*HumanGateCohort, *errx.Error) {
	if actorID == uuid.Nil {
		return nil, errx.ErrUnauthorized
	}
	if strings.TrimSpace(key) == "" {
		return nil, humanGateError(errx.BadRequest, "idempotency_key_required", "Idempotency-Key is required")
	}
	unlock, lockErr := s.lockHumanGateIntent(ctx, orgID, key)
	if lockErr != nil {
		return nil, lockErr
	}
	defer unlock()
	reqHash := humanGateRequestHash(struct {
		V, C uuid.UUID
	}{versionID, candidateID})
	var priorHash, priorReceipt string
	if err := s.humanGateDB.QueryRow(ctx, `SELECT request_hash,receipt FROM confenge_cohort_validations WHERE organization_id=$1 AND idempotency_key=$2`, orgID, key).Scan(&priorHash, &priorReceipt); err == nil {
		if priorHash != reqHash {
			return nil, humanGateError(errx.Conflict, "idempotency_payload_conflict", "Idempotency-Key was already used with another payload")
		}
		return s.humanGateWithOperationReceipt(ctx, orgID, versionID, priorReceipt)
	} else if err != pgx.ErrNoRows {
		return nil, humanGateError(errx.Internal, "idempotency_read_failed", "idempotency receipt could not be read")
	}
	v, x := s.GetHumanGateCohort(ctx, orgID, versionID, time.Now().UTC())
	if x != nil {
		return nil, x
	}
	var member *FrozenCohortMember
	for i := range v.Manifest.Members {
		if v.Manifest.Members[i].CandidateID == candidateID {
			member = &v.Manifest.Members[i]
			break
		}
	}
	if member == nil {
		return nil, humanGateError(errx.NotFound, "candidate_not_found", "candidate is not in this immutable version")
	}
	status := normalizeValidationStatus(result.Status)
	checked := result.CheckedAt.UTC()
	if checked.IsZero() {
		checked = time.Now().UTC()
	}
	expires := checked.Add(HumanGateValidationTTL)
	evidence := HashCohortID(member.EvidenceHash, member.Mailbox, status, result.Reason, checked.Format(time.RFC3339Nano))
	id := uuid.New()
	receipt := humanGateReceipt("validation", id)
	_, err := s.humanGateDB.Exec(ctx, `INSERT INTO confenge_cohort_validations(id,organization_id,cohort_version_id,candidate_id,status,reason,provider,method,evidence_hash,checked_at,expires_at,actor_id,correlation_id,receipt,idempotency_key,request_hash) VALUES($1,$2,$3,$4,$5,$6,'warmbly-emailverify','syntax-mx-smtp',$7,$8,$9,$10,$11,$12,$13,$14)`, id, orgID, versionID, candidateID, status, result.Reason, evidence, checked, expires, actorID, correlation, receipt, key, reqHash)
	if err != nil {
		var stored, existingReceipt string
		var existing uuid.UUID
		e := s.humanGateDB.QueryRow(ctx, `SELECT id,request_hash,receipt FROM confenge_cohort_validations WHERE organization_id=$1 AND idempotency_key=$2`, orgID, key).Scan(&existing, &stored, &existingReceipt)
		if e == nil && stored == reqHash {
			return s.humanGateWithOperationReceipt(ctx, orgID, versionID, existingReceipt)
		}
		if e == nil {
			return nil, humanGateError(errx.Conflict, "idempotency_payload_conflict", "Idempotency-Key was already used with another payload")
		}
		return nil, humanGateError(errx.Internal, "validation_store_failed", "validation receipt could not be stored")
	}
	s.auditHumanGate(ctx, orgID, actorID, "candidate_validation_recorded", candidateID, map[string]string{"status": "UNKNOWN"}, map[string]string{"status": status, "evidence_hash": evidence})
	return s.humanGateWithOperationReceipt(ctx, orgID, versionID, receipt)
}

func (s *service) ReviewHumanGateCandidate(ctx context.Context, orgID, actorID, versionID, candidateID uuid.UUID, in HumanGateReviewInput) (*HumanGateCohort, *errx.Error) {
	if actorID == uuid.Nil {
		return nil, errx.ErrUnauthorized
	}
	in.Decision = strings.ToUpper(strings.TrimSpace(in.Decision))
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Decision != "APPROVE" && in.Decision != "REJECT" && in.Decision != "HOLD" {
		return nil, humanGateError(errx.BadRequest, "invalid_review_decision", "decision must be APPROVE, REJECT or HOLD")
	}
	if in.Reason == "" {
		return nil, humanGateError(errx.BadRequest, "review_reason_required", "reason is required")
	}
	if in.Decision == "APPROVE" && !in.Acknowledged {
		return nil, humanGateError(errx.BadRequest, "approval_acknowledgement_required", "APPROVE requires explicit acknowledgement of recipient, message, policy and validation evidence")
	}
	if in.Decision != "APPROVE" {
		in.Acknowledged = false
	}
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	if in.IdempotencyKey == "" {
		return nil, humanGateError(errx.BadRequest, "idempotency_key_required", "Idempotency-Key is required")
	}
	unlock, lockErr := s.lockHumanGateIntent(ctx, orgID, in.IdempotencyKey)
	if lockErr != nil {
		return nil, lockErr
	}
	defer unlock()
	reqHash := humanGateRequestHash(struct {
		V, C uuid.UUID
		D, R string
		A    bool
	}{versionID, candidateID, in.Decision, in.Reason, in.Acknowledged})
	var priorHash, priorReceipt string
	if err := s.humanGateDB.QueryRow(ctx, `SELECT request_hash,receipt FROM confenge_cohort_candidate_reviews WHERE organization_id=$1 AND idempotency_key=$2`, orgID, in.IdempotencyKey).Scan(&priorHash, &priorReceipt); err == nil {
		if priorHash != reqHash {
			return nil, humanGateError(errx.Conflict, "idempotency_payload_conflict", "Idempotency-Key was already used with another payload")
		}
		if in.Decision == "APPROVE" {
			if _, _, scheduleErr := s.scheduleLatestHumanGateApproval(ctx, orgID, versionID, candidateID); scheduleErr != nil {
				return nil, scheduleErr
			}
		} else {
			s.invalidateHumanGateCandidateScheduling(ctx, orgID, versionID, candidateID, "human_gate_decision_"+strings.ToLower(in.Decision))
		}
		return s.humanGateWithOperationReceipt(ctx, orgID, versionID, priorReceipt)
	} else if err != pgx.ErrNoRows {
		return nil, humanGateError(errx.Internal, "idempotency_read_failed", "idempotency receipt could not be read")
	}
	v, x := s.GetHumanGateCohort(ctx, orgID, versionID, time.Now().UTC())
	if x != nil {
		return nil, x
	}
	var c *HumanGateCandidate
	for i := range v.Candidates {
		if v.Candidates[i].CandidateID == candidateID {
			c = &v.Candidates[i]
			break
		}
	}
	if c == nil {
		return nil, humanGateError(errx.NotFound, "candidate_not_found", "candidate is not in this immutable version")
	}
	if in.Decision == "APPROVE" {
		// History is readable, never approvable. HOLD and REJECT stay open so a
		// reviewer can still close an old version out.
		stamped := c.ComposerVersion
		if ComposerSuperseded(v.Manifest.ComposerVersion) {
			stamped = v.Manifest.ComposerVersion
		}
		if auth := EvaluateCohortEditorialAuthority(stamped, v.PolicyVersion); !auth.Actionable {
			return nil, humanGateError(errx.Conflict, ReasonComposerSuperseded, auth.Blocker("APPROVE"))
		}
	}
	if in.Decision == "APPROVE" && v.PolicyVersion != BoundedCohortPolicyV1 {
		return nil, humanGateError(errx.Conflict, "policy_drift", "APPROVE requires a cohort created under the current policy version")
	}
	if in.Decision == "APPROVE" && (c.Validation == nil || c.Validation.Status != "VALID") {
		return nil, humanGateError(errx.Conflict, "validation_blocks_approval", "APPROVE requires a current VALID result")
	}
	id := uuid.New()
	receipt := humanGateReceipt("review", id)
	before, _ := json.Marshal(map[string]any{"decision": func() string {
		if c.Review != nil {
			return c.Review.Decision
		}
		return "NONE"
	}()})
	after, _ := json.Marshal(map[string]any{"decision": in.Decision})
	var validationID any
	var validationExpiry any
	if c.Validation != nil {
		validationID = c.Validation.ID
		validationExpiry = c.Validation.ExpiresAt
	}
	_, err := s.humanGateDB.Exec(ctx, `INSERT INTO confenge_cohort_candidate_reviews(id,organization_id,cohort_version_id,candidate_id,decision,reason,recipient_hash,content_hash,policy_version,evidence_hash,validation_id,validation_expires_at,actor_id,correlation_id,receipt,idempotency_key,request_hash,before_state,after_state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`, id, orgID, versionID, candidateID, in.Decision, in.Reason, HashRecipientSet([]string{c.Mailbox}), c.ContentHash, v.PolicyVersion, c.EvidenceHash, validationID, validationExpiry, actorID, in.CorrelationID, receipt, in.IdempotencyKey, reqHash, before, after)
	if err != nil {
		var stored, existingReceipt string
		e := s.humanGateDB.QueryRow(ctx, `SELECT request_hash,receipt FROM confenge_cohort_candidate_reviews WHERE organization_id=$1 AND idempotency_key=$2`, orgID, in.IdempotencyKey).Scan(&stored, &existingReceipt)
		if e == nil && stored == reqHash {
			if in.Decision == "APPROVE" {
				if _, _, scheduleErr := s.scheduleLatestHumanGateApproval(ctx, orgID, versionID, candidateID); scheduleErr != nil {
					return nil, scheduleErr
				}
			} else {
				s.invalidateHumanGateCandidateScheduling(ctx, orgID, versionID, candidateID, "human_gate_decision_"+strings.ToLower(in.Decision))
			}
			return s.humanGateWithOperationReceipt(ctx, orgID, versionID, existingReceipt)
		}
		if e == nil {
			return nil, humanGateError(errx.Conflict, "idempotency_payload_conflict", "Idempotency-Key was already used with another payload")
		}
		return nil, humanGateError(errx.Internal, "review_store_failed", "review receipt could not be stored")
	}
	if in.Decision == "APPROVE" {
		if _, _, scheduleErr := s.scheduleLatestHumanGateApproval(ctx, orgID, versionID, candidateID); scheduleErr != nil {
			return nil, scheduleErr
		}
	} else {
		s.invalidateHumanGateCandidateScheduling(ctx, orgID, versionID, candidateID, "human_gate_decision_"+strings.ToLower(in.Decision))
	}
	s.auditHumanGate(ctx, orgID, actorID, "candidate_review_recorded", candidateID, map[string]string{"decision": func() string {
		if c.Review != nil {
			return c.Review.Decision
		}
		return "NONE"
	}()}, map[string]string{"decision": in.Decision, "content_hash": c.ContentHash})
	return s.humanGateWithOperationReceipt(ctx, orgID, versionID, receipt)
}

func humanGateDecisionBlockers(v *HumanGateCohort) []string {
	blocked := []string{}
	if v == nil || len(v.Candidates) == 0 {
		blocked = append(blocked, "cohort_empty")
	}
	// GO mints send authority, so it is refused outright on superseded copy.
	if v != nil {
		if auth := SnapshotEditorialAuthority(&v.Manifest); !auth.Actionable {
			blocked = append(blocked, auth.ReasonCodes...)
		}
	}
	if v == nil || v.Freshness != "FRESH" {
		blocked = append(blocked, "source_evidence_stale")
	}
	if v != nil {
		for _, c := range v.Candidates {
			if c.Validation == nil || c.Validation.Status != "VALID" || c.Review == nil || !c.Review.Effective || c.Review.Decision != "APPROVE" || len(c.BlockedBy) > 0 {
				blocked = append(blocked, "candidate_not_approved:"+c.CandidateID.String())
			}
		}
	}
	return blocked
}

func (s *service) latestHumanGateValidation(ctx context.Context, orgID, versionID, candidateID uuid.UUID, now time.Time) *HumanGateValidation {
	v := &HumanGateValidation{}
	err := s.humanGateDB.QueryRow(ctx, `SELECT id,status,reason,provider,method,evidence_hash,checked_at,expires_at,correlation_id,receipt FROM confenge_cohort_validations WHERE organization_id=$1 AND cohort_version_id=$2 AND candidate_id=$3 ORDER BY created_at DESC,id DESC LIMIT 1`, orgID, versionID, candidateID).Scan(&v.ID, &v.Status, &v.Reason, &v.Provider, &v.Method, &v.EvidenceHash, &v.CheckedAt, &v.ExpiresAt, &v.Correlation, &v.Receipt)
	if err != nil {
		return nil
	}
	if !now.Before(v.ExpiresAt) {
		v.Status = "STALE"
		v.Reason = "validation_evidence_expired"
	}
	return v
}

func (s *service) latestHumanGateReview(ctx context.Context, orgID, versionID uuid.UUID, m FrozenCohortMember, expectedPolicy string, v *HumanGateValidation, now time.Time) *HumanGateReview {
	r := &HumanGateReview{Effective: true, InvalidatedBy: []string{}}
	var recipient, content, policy, evidence string
	var validationID *uuid.UUID
	var validationExpires *time.Time
	err := s.humanGateDB.QueryRow(ctx, `SELECT id,decision,reason,recipient_hash,content_hash,policy_version,evidence_hash,validation_id,validation_expires_at,actor_id,created_at,correlation_id,receipt FROM confenge_cohort_candidate_reviews WHERE organization_id=$1 AND cohort_version_id=$2 AND candidate_id=$3 ORDER BY created_at DESC,id DESC LIMIT 1`, orgID, versionID, m.CandidateID).Scan(&r.ID, &r.Decision, &r.Reason, &recipient, &content, &policy, &evidence, &validationID, &validationExpires, &r.ActorID, &r.CreatedAt, &r.Correlation, &r.Receipt)
	if err != nil {
		return nil
	}
	evaluateHumanGateReview(r, m, expectedPolicy, recipient, content, policy, evidence, validationID, validationExpires, v, now)
	return r
}

func evaluateHumanGateReview(r *HumanGateReview, m FrozenCohortMember, expectedPolicy, recipient, content, policy, evidence string, validationID *uuid.UUID, validationExpires *time.Time, v *HumanGateValidation, now time.Time) {
	if r == nil {
		return
	}
	r.Effective = r.Decision == "APPROVE"
	if recipient != HashRecipientSet([]string{m.Mailbox}) {
		r.InvalidatedBy = append(r.InvalidatedBy, "recipient_drift")
	}
	if content != m.ContentHash {
		r.InvalidatedBy = append(r.InvalidatedBy, "message_drift")
	}
	if policy != expectedPolicy {
		r.InvalidatedBy = append(r.InvalidatedBy, "policy_drift")
	}
	if evidence != m.EvidenceHash {
		r.InvalidatedBy = append(r.InvalidatedBy, "evidence_drift")
	}
	if v == nil || validationID == nil || v.ID != *validationID || v.Status != "VALID" {
		r.InvalidatedBy = append(r.InvalidatedBy, "validation_changed")
	}
	if validationExpires == nil || !now.Before(*validationExpires) {
		r.InvalidatedBy = append(r.InvalidatedBy, "validation_expired")
	}
	if len(r.InvalidatedBy) > 0 {
		r.Effective = false
	}
}

func (s *service) latestHumanGateDecision(ctx context.Context, orgID, versionID uuid.UUID) *HumanGateDecision {
	d := &HumanGateDecision{}
	if s.humanGateDB.QueryRow(ctx, `SELECT id,decision,reason,authorization_id,actor_id,created_at,correlation_id,receipt FROM confenge_cohort_go_decisions WHERE organization_id=$1 AND cohort_version_id=$2 ORDER BY created_at DESC,id DESC LIMIT 1`, orgID, versionID).Scan(&d.ID, &d.Decision, &d.Reason, &d.AuthorizationID, &d.ActorID, &d.CreatedAt, &d.Correlation, &d.Receipt) != nil {
		return nil
	}
	if d.Decision == "GO" && d.AuthorizationID != nil {
		d.QueueState = "READY_FOR_LIVE_PREFLIGHT"
	} else {
		d.QueueState = "BLOCKED"
	}
	return d
}

func (s *service) auditHumanGate(ctx context.Context, orgID, actorID uuid.UUID, action string, entityID uuid.UUID, before, after map[string]string) {
	if s.audit == nil {
		return
	}
	meta := map[string]string{"contract_version": HumanGateContractV1}
	for k, v := range after {
		meta["after_"+k] = v
	}
	for k, v := range before {
		meta["before_"+k] = v
	}
	s.audit.LogAction(ctx, orgID, actorID, models.AuditAction(action), models.AuditEntityType("confenge_human_gate"), &entityID, "", "", nil, meta)
}
