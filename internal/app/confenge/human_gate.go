package confenge

import (
	"context"
	"crypto/sha256"
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
	ContractVersion string               `json:"contract_version"`
	ID              uuid.UUID            `json:"id"`
	CohortID        uuid.UUID            `json:"cohort_id"`
	Version         int                  `json:"version"`
	Source          string               `json:"source"`
	SourceRunID     string               `json:"source_run_id"`
	AsOf            time.Time            `json:"as_of"`
	Freshness       string               `json:"freshness"`
	FreshUntil      time.Time            `json:"fresh_until"`
	PolicyVersion   string               `json:"policy_version"`
	FrozenHash      string               `json:"frozen_hash"`
	Manifest        FrozenCohortSnapshot `json:"manifest"`
	Candidates      []HumanGateCandidate `json:"candidates"`
	Decision        *HumanGateDecision   `json:"decision"`
	Reason          []string             `json:"reason"`
	CorrelationID   string               `json:"correlation_id"`
	Receipt         string               `json:"receipt"`
	CreatedAt       time.Time            `json:"created_at"`
}

type HumanGateReviewInput struct {
	Decision       string `json:"decision"`
	Reason         string `json:"reason"`
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

func humanGateError(code errx.Code, id, message string) *errx.Error {
	return errx.NewWithIdentifier(code, id, message)
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
	if strings.TrimSpace(in.IdempotencyKey) == "" {
		return nil, humanGateError(errx.BadRequest, "idempotency_key_required", "Idempotency-Key is required")
	}
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
		(id,organization_id,cohort_id,version,source_run_id,source_system,source_as_of,freshness_expires_at,policy_version,frozen_hash,frozen_manifest,reproduced_from_version,created_by,correlation_id,idempotency_key,request_hash)
		SELECT $1,organization_id,cohort_id,(SELECT max(version)+1 FROM confenge_cohort_versions WHERE organization_id=$2 AND cohort_id=$3),source_run_id,source_system,source_as_of,freshness_expires_at,policy_version,frozen_hash,$4,version,$5,$6,$7,$8
		FROM confenge_cohort_versions WHERE id=$9 AND organization_id=$2 RETURNING version`, newID, orgID, old.CohortID, b, actorID, in.CorrelationID, in.IdempotencyKey, reqHash, id).Scan(&version)
	if err != nil {
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
	err := s.humanGateDB.QueryRow(ctx, `SELECT cohort_id,version,source_run_id,source_system,source_as_of,freshness_expires_at,policy_version,frozen_hash,frozen_manifest,correlation_id,created_at FROM confenge_cohort_versions WHERE id=$1 AND organization_id=$2`, id, orgID).Scan(&v.CohortID, &v.Version, &v.SourceRunID, &v.Source, &v.AsOf, &v.FreshUntil, &v.PolicyVersion, &v.FrozenHash, &raw, &v.CorrelationID, &v.CreatedAt)
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
	v.Candidates = make([]HumanGateCandidate, 0, len(v.Manifest.Members))
	current := map[uuid.UUID]CohortAccountInput{}
	if accounts, liveErr := AccountsFromOrgForRun(ctx, s.repo, orgID, v.Source, v.SourceRunID); liveErr == nil {
		for _, account := range accounts {
			for _, candidate := range account.Candidates {
				current[candidate.ID] = account
			}
		}
	} else {
		v.Reason = append(v.Reason, "live_suppression_state_unknown")
	}
	for _, m := range v.Manifest.Members {
		c := HumanGateCandidate{FrozenCohortMember: m, BlockedBy: []string{}}
		c.Validation = s.latestHumanGateValidation(ctx, orgID, id, m.CandidateID, now)
		c.Review = s.latestHumanGateReview(ctx, orgID, id, m, c.Validation, now)
		if c.Validation == nil {
			c.BlockedBy = append(c.BlockedBy, "validation_missing")
		} else if c.Validation.Status != "VALID" {
			c.BlockedBy = append(c.BlockedBy, "validation_"+strings.ToLower(c.Validation.Status))
		}
		if c.Review == nil || !c.Review.Effective {
			c.BlockedBy = append(c.BlockedBy, "approval_missing_or_invalid")
		}
		if live, ok := current[m.CandidateID]; ok {
			var candidate *models.OutreachContactCandidate
			for i := range live.Candidates {
				if live.Candidates[i].ID == m.CandidateID {
					candidate = &live.Candidates[i]
					break
				}
			}
			liveReasons := []string{}
			if live.Account.Blocked {
				liveReasons = append(liveReasons, "late_account_suppression")
			}
			if live.Account.DoNotContact {
				liveReasons = append(liveReasons, "late_account_opt_out")
			}
			if candidate == nil {
				liveReasons = append(liveReasons, "candidate_removed")
			} else {
				if candidate.Blocked {
					liveReasons = append(liveReasons, "late_recipient_suppression")
				}
				if candidate.DoNotContact {
					liveReasons = append(liveReasons, "late_recipient_opt_out")
				}
				if candidate.Bounced {
					liveReasons = append(liveReasons, "late_hard_bounce")
				}
				if canonicalPilotEmail(candidate.Email) != canonicalPilotEmail(m.Mailbox) {
					liveReasons = append(liveReasons, "recipient_drift")
				}
			}
			if len(liveReasons) > 0 {
				c.BlockedBy = append(c.BlockedBy, liveReasons...)
				if c.Review != nil {
					c.Review.Effective = false
					c.Review.InvalidatedBy = append(c.Review.InvalidatedBy, liveReasons...)
				}
			}
		} else {
			c.BlockedBy = append(c.BlockedBy, "live_candidate_state_unknown")
			if c.Review != nil {
				c.Review.Effective = false
				c.Review.InvalidatedBy = append(c.Review.InvalidatedBy, "live_candidate_state_unknown")
			}
		}
		v.Candidates = append(v.Candidates, c)
	}
	v.Decision = s.latestHumanGateDecision(ctx, orgID, id)
	return v, nil
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
	if strings.TrimSpace(key) == "" {
		return nil, humanGateError(errx.BadRequest, "idempotency_key_required", "Idempotency-Key is required")
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
	reqHash := humanGateRequestHash(struct {
		V, C uuid.UUID
		S, R string
	}{versionID, candidateID, status, result.Reason})
	_, err := s.humanGateDB.Exec(ctx, `INSERT INTO confenge_cohort_validations(id,organization_id,cohort_version_id,candidate_id,status,reason,provider,method,evidence_hash,checked_at,expires_at,actor_id,correlation_id,receipt,idempotency_key,request_hash) VALUES($1,$2,$3,$4,$5,$6,'warmbly-emailverify','syntax-mx-smtp',$7,$8,$9,$10,$11,$12,$13,$14)`, id, orgID, versionID, candidateID, status, result.Reason, evidence, checked, expires, actorID, correlation, humanGateReceipt("validation", id), key, reqHash)
	if err != nil {
		var stored string
		var existing uuid.UUID
		e := s.humanGateDB.QueryRow(ctx, `SELECT id,request_hash FROM confenge_cohort_validations WHERE organization_id=$1 AND idempotency_key=$2`, orgID, key).Scan(&existing, &stored)
		if e == nil && stored == reqHash {
			return s.GetHumanGateCohort(ctx, orgID, versionID, time.Now().UTC())
		}
		if e == nil {
			return nil, humanGateError(errx.Conflict, "idempotency_payload_conflict", "Idempotency-Key was already used with another payload")
		}
		return nil, humanGateError(errx.Internal, "validation_store_failed", "validation receipt could not be stored")
	}
	s.auditHumanGate(ctx, orgID, actorID, "candidate_validation_recorded", candidateID, map[string]string{"status": "UNKNOWN"}, map[string]string{"status": status, "evidence_hash": evidence})
	return s.GetHumanGateCohort(ctx, orgID, versionID, time.Now().UTC())
}

func (s *service) ReviewHumanGateCandidate(ctx context.Context, orgID, actorID, versionID, candidateID uuid.UUID, in HumanGateReviewInput) (*HumanGateCohort, *errx.Error) {
	in.Decision = strings.ToUpper(strings.TrimSpace(in.Decision))
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Decision != "APPROVE" && in.Decision != "REJECT" && in.Decision != "HOLD" {
		return nil, humanGateError(errx.BadRequest, "invalid_review_decision", "decision must be APPROVE, REJECT or HOLD")
	}
	if in.Reason == "" {
		return nil, humanGateError(errx.BadRequest, "review_reason_required", "reason is required")
	}
	if in.IdempotencyKey == "" {
		return nil, humanGateError(errx.BadRequest, "idempotency_key_required", "Idempotency-Key is required")
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
	if in.Decision == "APPROVE" && (c.Validation == nil || c.Validation.Status != "VALID") {
		return nil, humanGateError(errx.Conflict, "validation_blocks_approval", "APPROVE requires a current VALID result")
	}
	id := uuid.New()
	reqHash := humanGateRequestHash(struct {
		V, C uuid.UUID
		D, R string
	}{versionID, candidateID, in.Decision, in.Reason})
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
	_, err := s.humanGateDB.Exec(ctx, `INSERT INTO confenge_cohort_candidate_reviews(id,organization_id,cohort_version_id,candidate_id,decision,reason,recipient_hash,content_hash,policy_version,evidence_hash,validation_id,validation_expires_at,actor_id,correlation_id,receipt,idempotency_key,request_hash,before_state,after_state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`, id, orgID, versionID, candidateID, in.Decision, in.Reason, HashRecipientSet([]string{c.Mailbox}), c.ContentHash, v.PolicyVersion, c.EvidenceHash, validationID, validationExpiry, actorID, in.CorrelationID, humanGateReceipt("review", id), in.IdempotencyKey, reqHash, before, after)
	if err != nil {
		var stored string
		e := s.humanGateDB.QueryRow(ctx, `SELECT request_hash FROM confenge_cohort_candidate_reviews WHERE organization_id=$1 AND idempotency_key=$2`, orgID, in.IdempotencyKey).Scan(&stored)
		if e == nil && stored == reqHash {
			return s.GetHumanGateCohort(ctx, orgID, versionID, time.Now().UTC())
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
	return s.GetHumanGateCohort(ctx, orgID, versionID, time.Now().UTC())
}

func (s *service) DecideHumanGateCohort(ctx context.Context, orgID, actorID, versionID uuid.UUID, in HumanGateDecisionInput) (*HumanGateCohort, *errx.Error) {
	in.Decision = strings.ToUpper(strings.TrimSpace(in.Decision))
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Decision != "GO" && in.Decision != "NO_GO" {
		return nil, humanGateError(errx.BadRequest, "invalid_cohort_decision", "decision must be GO or NO_GO")
	}
	if in.Reason == "" || in.IdempotencyKey == "" {
		return nil, humanGateError(errx.BadRequest, "decision_fields_required", "reason and Idempotency-Key are required")
	}
	v, x := s.GetHumanGateCohort(ctx, orgID, versionID, time.Now().UTC())
	if x != nil {
		return nil, x
	}
	blocked := []string{}
	if len(v.Candidates) == 0 {
		blocked = append(blocked, "cohort_empty")
	}
	if v.Freshness != "FRESH" {
		blocked = append(blocked, "source_evidence_stale")
	}
	for _, c := range v.Candidates {
		if c.Review == nil || !c.Review.Effective || c.Review.Decision != "APPROVE" {
			blocked = append(blocked, "candidate_not_approved:"+c.CandidateID.String())
		}
	}
	if in.Decision == "GO" && len(blocked) > 0 {
		return nil, humanGateError(errx.Conflict, "cohort_not_ready", strings.Join(blocked, ","))
	}
	sort.Strings(blocked)
	readiness := humanGateRequestHash(struct {
		H string
		B []string
	}{v.FrozenHash, blocked})
	id := uuid.New()
	reqHash := humanGateRequestHash(struct {
		V    uuid.UUID
		D, R string
	}{versionID, in.Decision, in.Reason})
	var existingHash string
	if err := s.humanGateDB.QueryRow(ctx, `SELECT request_hash FROM confenge_cohort_go_decisions WHERE organization_id=$1 AND idempotency_key=$2`, orgID, in.IdempotencyKey).Scan(&existingHash); err == nil {
		if existingHash != reqHash {
			return nil, humanGateError(errx.Conflict, "idempotency_payload_conflict", "Idempotency-Key was already used with another payload")
		}
		return s.GetHumanGateCohort(ctx, orgID, versionID, time.Now().UTC())
	} else if err != pgx.ErrNoRows {
		return nil, humanGateError(errx.Internal, "idempotency_read_failed", "idempotency receipt could not be read")
	}
	var authorizationID *uuid.UUID
	if in.Decision == "GO" {
		authorizeAt := time.Now().UTC()
		authorizedSnapshot := v.Manifest
		expiresAt := authorizeAt.Add(time.Duration(authorizedSnapshot.TTLSeconds) * time.Second)
		if authorizedSnapshot.TTLSeconds <= 0 {
			expiresAt = authorizeAt.Add(DefaultCohortTTL)
		}
		if v.FreshUntil.Before(expiresAt) {
			expiresAt = v.FreshUntil
		}
		for _, candidate := range v.Candidates {
			if candidate.Validation != nil && candidate.Validation.ExpiresAt.Before(expiresAt) {
				expiresAt = candidate.Validation.ExpiresAt
			}
		}
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
		if err := s.cohortStore.RecordGOReview(ctx, authID, actorID, ReleaseReadyForControlledEmailReview, in.Reason, time.Now().UTC()); err != nil {
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
	_, err := s.humanGateDB.Exec(ctx, `INSERT INTO confenge_cohort_go_decisions(id,organization_id,cohort_version_id,decision,reason,readiness_hash,authorization_id,actor_id,correlation_id,receipt,idempotency_key,request_hash,before_state,after_state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, id, orgID, versionID, in.Decision, in.Reason, readiness, authorizationID, actorID, in.CorrelationID, humanGateReceipt("decision", id), in.IdempotencyKey, reqHash, before, after)
	if err != nil {
		if authorizationID != nil {
			_ = s.cohortStore.RevokeGrant(ctx, *authorizationID, actorID, "human_gate_decision_store_failed", time.Now().UTC())
		}
		var stored string
		e := s.humanGateDB.QueryRow(ctx, `SELECT request_hash FROM confenge_cohort_go_decisions WHERE organization_id=$1 AND idempotency_key=$2`, orgID, in.IdempotencyKey).Scan(&stored)
		if e == nil && stored == reqHash {
			return s.GetHumanGateCohort(ctx, orgID, versionID, time.Now().UTC())
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
	return s.GetHumanGateCohort(ctx, orgID, versionID, time.Now().UTC())
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

func (s *service) latestHumanGateReview(ctx context.Context, orgID, versionID uuid.UUID, m FrozenCohortMember, v *HumanGateValidation, now time.Time) *HumanGateReview {
	r := &HumanGateReview{Effective: true, InvalidatedBy: []string{}}
	var recipient, content, policy, evidence string
	var validationID *uuid.UUID
	var validationExpires *time.Time
	err := s.humanGateDB.QueryRow(ctx, `SELECT id,decision,reason,recipient_hash,content_hash,policy_version,evidence_hash,validation_id,validation_expires_at,actor_id,created_at,correlation_id,receipt FROM confenge_cohort_candidate_reviews WHERE organization_id=$1 AND cohort_version_id=$2 AND candidate_id=$3 ORDER BY created_at DESC,id DESC LIMIT 1`, orgID, versionID, m.CandidateID).Scan(&r.ID, &r.Decision, &r.Reason, &recipient, &content, &policy, &evidence, &validationID, &validationExpires, &r.ActorID, &r.CreatedAt, &r.Correlation, &r.Receipt)
	if err != nil {
		return nil
	}
	evaluateHumanGateReview(r, m, BoundedCohortPolicyV1, recipient, content, policy, evidence, validationID, validationExpires, v, now)
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
