package confenge

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
	Limit          int    `json:"limit"`
	SourceRunID    string `json:"source_run_id,omitempty"`
	IdempotencyKey string `json:"-"`
	CorrelationID  string `json:"-"`
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
	BlockedBy  []string             `json:"blocked_by"`
}

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
	Candidates       []HumanGateCandidate `json:"candidates"`
	Decision         *HumanGateDecision   `json:"decision"`
	Reason           []string             `json:"reason"`
	CorrelationID    string               `json:"correlation_id"`
	Receipt          string               `json:"receipt"`
	OperationReceipt string               `json:"-"`
	CreatedAt        time.Time            `json:"created_at"`
}

type HumanGateReviewInput struct {
	Decision       string `json:"decision"`
	Reason         string `json:"reason"`
	Acknowledged   bool   `json:"acknowledged,omitempty"`
	Confirmation   string `json:"confirmation,omitempty"`
	IdempotencyKey string `json:"-"`
	CorrelationID  string `json:"-"`
}

type HumanGateDecisionInput = HumanGateReviewInput

func (s *service) WireHumanGate(db *pgxpool.Pool) { s.humanGateDB = db }

func humanGateRequestHash(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func humanGateReceipt(kind string, id uuid.UUID) string { return kind + ":" + id.String() }

func humanGateAuthorizationID(orgID, versionID uuid.UUID, idempotencyKey string) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(HumanGateContractV1+"\x00"+orgID.String()+"\x00"+versionID.String()+"\x00"+strings.TrimSpace(idempotencyKey)))
}

func humanGateVersionConfirmed(confirmation string, version int) bool {
	return strings.ToLower(strings.TrimSpace(confirmation)) == fmt.Sprintf("v%d", version)
}

func (s *service) requireDurableHumanGateGO(ctx context.Context, orgID uuid.UUID, auth *BoundedCohortAuthorization) *errx.Error {
	if auth == nil || auth.GOReviewVerdict != HumanGateReadyVerdict {
		return nil // backward-compatible: this guard owns only authorities created by this contract.
	}
	if s.humanGateDB == nil {
		return humanGateError(errx.Conflict, "human_gate_decision_missing", "human-gate authority has no durable GO decision store")
	}
	var exists bool
	if err := s.humanGateDB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM confenge_cohort_go_decisions WHERE organization_id=$1 AND authorization_id=$2 AND decision='GO')`, orgID, auth.ID).Scan(&exists); err != nil || !exists {
		return humanGateError(errx.Conflict, "human_gate_decision_missing", "human-gate authority is not bound to a durable GO decision")
	}
	return nil
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
	reqHash := humanGateRequestHash(struct {
		Limit int
		Run   string
	}{in.Limit, strings.TrimSpace(in.SourceRunID)})
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
	accounts, err := AccountsFromOrgForRun(ctx, s.repo, orgID, run.SourceSystem, run.SourceRunID)
	if err != nil {
		return nil, humanGateError(errx.Unprocessable, "source_accounts_unavailable", err.Error())
	}
	freshness := &FeedSourceFreshness{ContractVersion: AuthoritativeFreshnessContractV1, Status: "FRESH", AsOf: asOf.Format(time.RFC3339Nano), ExpiresAt: asOf.Add(maxAge).Format(time.RFC3339Nano), DeployedSHA: run.RepoSHA, PolicyVersion: run.ProfileVersion, RunID: run.SourceRunID}
	manifest, err := PrepareControlledCohort(accounts, CohortPrepareOptions{
		Now: now, Limit: in.Limit, MaxDailyVolume: in.Limit, TTL: DefaultCohortTTL,
		RepositorySHA: s.cfg.RepositorySHA, FeedSchemaVersion: firstNonEmpty(s.cfg.FeedSchemaVersion, run.SchemaVersion),
		FeedIdentity: run.SourceRunID, SnapshotHash: run.SnapshotHash, PolicyVersion: BoundedCohortPolicyV1,
		EvidenceVersion: firstNonEmpty(s.cfg.EvidenceVersion, DefaultEvidenceVersion), Source: run.SourceSystem,
		AuthoritativeSourceFreshness: freshness, RequireAuthoritativeFreshness: true,
	})
	if err != nil {
		return nil, humanGateError(errx.Unprocessable, "cohort_prepare_failed", err.Error())
	}
	cohortID, versionID := uuid.New(), uuid.New()
	b, _ := json.Marshal(manifest)
	_, err = s.humanGateDB.Exec(ctx, `INSERT INTO confenge_cohort_versions
		(id,organization_id,cohort_id,version,source_run_id,source_system,source_as_of,freshness_expires_at,policy_version,frozen_hash,frozen_manifest,created_by,correlation_id,idempotency_key,request_hash)
		VALUES($1,$2,$3,1,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, versionID, orgID, cohortID,
		run.SourceRunID, run.SourceSystem, asOf, asOf.Add(maxAge), manifest.PolicyVersion, manifest.CohortHash, b, actorID,
		strings.TrimSpace(in.CorrelationID), strings.TrimSpace(in.IdempotencyKey), reqHash)
	if err != nil {
		if existing, x := s.humanGateByIdempotency(ctx, orgID, in.IdempotencyKey, reqHash); existing != nil || x != nil {
			return existing, x
		}
		return nil, humanGateError(errx.Internal, "cohort_store_failed", "cohort version could not be stored")
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
		(id,organization_id,cohort_id,version,source_run_id,source_system,source_as_of,freshness_expires_at,policy_version,frozen_hash,frozen_manifest,reproduced_from_version,derivation,parent_version,created_by,correlation_id,idempotency_key,request_hash)
		SELECT $1,organization_id,cohort_id,(SELECT max(version)+1 FROM confenge_cohort_versions WHERE organization_id=$2 AND cohort_id=$3),source_run_id,source_system,source_as_of,freshness_expires_at,policy_version,frozen_hash,$4,version,'REPRODUCE',version,$5,$6,$7,$8
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
	var raw []byte
	err := s.humanGateDB.QueryRow(ctx, `SELECT cohort_id,version,derivation,parent_version,source_run_id,source_system,source_as_of,freshness_expires_at,policy_version,frozen_hash,frozen_manifest,correlation_id,created_at FROM confenge_cohort_versions WHERE id=$1 AND organization_id=$2`, id, orgID).Scan(&v.CohortID, &v.Version, &v.Derivation, &v.ParentVersion, &v.SourceRunID, &v.Source, &v.AsOf, &v.FreshUntil, &v.PolicyVersion, &v.FrozenHash, &raw, &v.CorrelationID, &v.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, humanGateError(errx.NotFound, "cohort_version_not_found", "cohort version was not found")
	}
	if err != nil || json.Unmarshal(raw, &v.Manifest) != nil {
		return nil, humanGateError(errx.Internal, "cohort_read_failed", "cohort version could not be read")
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
		v.Candidates = append(v.Candidates, c)
	}
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
			return s.humanGateWithOperationReceipt(ctx, orgID, versionID, existingReceipt)
		}
		if e == nil {
			return nil, humanGateError(errx.Conflict, "idempotency_payload_conflict", "Idempotency-Key was already used with another payload")
		}
		return nil, humanGateError(errx.Internal, "review_store_failed", "review receipt could not be stored")
	}
	s.auditHumanGate(ctx, orgID, actorID, "candidate_review_recorded", candidateID, map[string]string{"decision": func() string {
		if c.Review != nil {
			return c.Review.Decision
		}
		return "NONE"
	}()}, map[string]string{"decision": in.Decision, "content_hash": c.ContentHash})
	return s.humanGateWithOperationReceipt(ctx, orgID, versionID, receipt)
}

func (s *service) DecideHumanGateCohort(ctx context.Context, orgID, actorID, versionID uuid.UUID, in HumanGateDecisionInput) (*HumanGateCohort, *errx.Error) {
	if actorID == uuid.Nil {
		return nil, errx.ErrUnauthorized
	}
	in.Decision = strings.ToUpper(strings.TrimSpace(in.Decision))
	in.Reason = strings.TrimSpace(in.Reason)
	in.Confirmation = strings.ToLower(strings.TrimSpace(in.Confirmation))
	if in.Decision != "GO" && in.Decision != "NO_GO" {
		return nil, humanGateError(errx.BadRequest, "invalid_cohort_decision", "decision must be GO or NO_GO")
	}
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	if in.Reason == "" || in.Confirmation == "" || in.IdempotencyKey == "" {
		return nil, humanGateError(errx.BadRequest, "decision_fields_required", "reason, confirmation and Idempotency-Key are required")
	}
	unlock, lockErr := s.lockHumanGateIntent(ctx, orgID, in.IdempotencyKey)
	if lockErr != nil {
		return nil, lockErr
	}
	defer unlock()
	reqHash := humanGateRequestHash(struct {
		V       uuid.UUID
		D, R, C string
	}{versionID, in.Decision, in.Reason, in.Confirmation})
	var existingHash, existingReceipt string
	if err := s.humanGateDB.QueryRow(ctx, `SELECT request_hash,receipt FROM confenge_cohort_go_decisions WHERE organization_id=$1 AND idempotency_key=$2`, orgID, in.IdempotencyKey).Scan(&existingHash, &existingReceipt); err == nil {
		if existingHash != reqHash {
			return nil, humanGateError(errx.Conflict, "idempotency_payload_conflict", "Idempotency-Key was already used with another payload")
		}
		return s.humanGateWithOperationReceipt(ctx, orgID, versionID, existingReceipt)
	} else if err != pgx.ErrNoRows {
		return nil, humanGateError(errx.Internal, "idempotency_read_failed", "idempotency receipt could not be read")
	}
	v, x := s.GetHumanGateCohort(ctx, orgID, versionID, time.Now().UTC())
	if x != nil {
		return nil, x
	}
	if !humanGateVersionConfirmed(in.Confirmation, v.Version) {
		return nil, humanGateError(errx.BadRequest, "cohort_version_confirmation_mismatch", "confirmation must match the immutable cohort version")
	}
	blocked := humanGateDecisionBlockers(v)
	if in.Decision == "GO" && len(blocked) > 0 {
		return nil, humanGateError(errx.Conflict, "cohort_not_ready", strings.Join(blocked, ","))
	}
	sort.Strings(blocked)
	readiness := humanGateRequestHash(struct {
		H string
		B []string
	}{v.FrozenHash, blocked})
	id := uuid.New()
	receipt := humanGateReceipt("decision", id)
	var authorizationID *uuid.UUID
	if in.Decision == "GO" {
		authorizeAt := time.Now().UTC()
		authorizedSnapshot := v.Manifest
		authorizedSnapshot.AuthorizationID = humanGateAuthorizationID(orgID, versionID, in.IdempotencyKey)
		expiresAt := humanGateAuthorizationExpiry(v, authorizeAt)
		if !authorizeAt.Before(expiresAt) {
			return nil, humanGateError(errx.Conflict, "cohort_evidence_expired", "validation or source evidence expired before authorization")
		}
		authorizedSnapshot.TTLSeconds = int64(expiresAt.Sub(authorizeAt) / time.Second)
		applied, applyErr := AuthorizeFrozenCohort(ctx, s.cohortStore, s.repo, orgID, actorID, &authorizedSnapshot, true, authorizeAt)
		if applyErr != nil {
			return nil, humanGateError(errx.Conflict, "cohort_live_gate_failed", applyErr.Error())
		}
		authID := applied.AuthorizationID
		authorizationID = &authID
		if err := s.cohortStore.RecordGOReview(ctx, authID, actorID, HumanGateReadyVerdict, in.Reason, time.Now().UTC()); err != nil {
			_ = s.cohortStore.RevokeGrant(ctx, authID, actorID, "human_gate_review_store_failed", time.Now().UTC())
			return nil, humanGateError(errx.ServiceUnavailable, "transport_authority_update_failed", "bounded transport authority could not record readiness")
		}
	} else if v.Decision != nil && v.Decision.AuthorizationID != nil {
		if err := s.cohortStore.RevokeGrant(ctx, *v.Decision.AuthorizationID, actorID, "human_no_go:"+in.Reason, time.Now().UTC()); err != nil {
			return nil, humanGateError(errx.ServiceUnavailable, "transport_authority_revoke_failed", "NO_GO was not recorded because the bounded authority could not be revoked")
		}
	}
	before, _ := json.Marshal(map[string]any{"decision": func() string {
		if v.Decision != nil {
			return v.Decision.Decision
		}
		return "NONE"
	}()})
	after, _ := json.Marshal(map[string]any{"decision": in.Decision, "readiness_hash": readiness})
	_, err := s.humanGateDB.Exec(ctx, `INSERT INTO confenge_cohort_go_decisions(id,organization_id,cohort_version_id,decision,reason,readiness_hash,authorization_id,actor_id,correlation_id,receipt,idempotency_key,request_hash,before_state,after_state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, id, orgID, versionID, in.Decision, in.Reason, readiness, authorizationID, actorID, in.CorrelationID, receipt, in.IdempotencyKey, reqHash, before, after)
	if err != nil {
		if authorizationID != nil {
			_ = s.cohortStore.RevokeGrant(ctx, *authorizationID, actorID, "human_gate_decision_store_failed", time.Now().UTC())
		}
		var stored, storedReceipt string
		e := s.humanGateDB.QueryRow(ctx, `SELECT request_hash,receipt FROM confenge_cohort_go_decisions WHERE organization_id=$1 AND idempotency_key=$2`, orgID, in.IdempotencyKey).Scan(&stored, &storedReceipt)
		if e == nil && stored == reqHash {
			return s.humanGateWithOperationReceipt(ctx, orgID, versionID, storedReceipt)
		}
		if e == nil {
			return nil, humanGateError(errx.Conflict, "idempotency_payload_conflict", "Idempotency-Key was already used with another payload")
		}
		return nil, humanGateError(errx.Internal, "decision_store_failed", "decision receipt could not be stored")
	}
	s.auditHumanGate(ctx, orgID, actorID, "cohort_go_decision_recorded", versionID, map[string]string{"decision": func() string {
		if v.Decision != nil {
			return v.Decision.Decision
		}
		return "NONE"
	}()}, map[string]string{"decision": in.Decision, "readiness_hash": readiness})
	return s.humanGateWithOperationReceipt(ctx, orgID, versionID, receipt)
}

func humanGateDecisionBlockers(v *HumanGateCohort) []string {
	blocked := []string{}
	if v == nil || len(v.Candidates) == 0 {
		blocked = append(blocked, "cohort_empty")
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

func humanGateAuthorizationExpiry(v *HumanGateCohort, authorizeAt time.Time) time.Time {
	expiresAt := authorizeAt.Add(DefaultCohortTTL)
	if v == nil {
		return expiresAt
	}
	if v.Manifest.TTLSeconds > 0 {
		expiresAt = authorizeAt.Add(time.Duration(v.Manifest.TTLSeconds) * time.Second)
	}
	if !v.FreshUntil.IsZero() && v.FreshUntil.Before(expiresAt) {
		expiresAt = v.FreshUntil
	}
	for _, candidate := range v.Candidates {
		if candidate.Validation != nil && !candidate.Validation.ExpiresAt.IsZero() && candidate.Validation.ExpiresAt.Before(expiresAt) {
			expiresAt = candidate.Validation.ExpiresAt
		}
	}
	return expiresAt
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
