package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/warmbly/warmbly/internal/models"
)

// OutreachRepository owns multi-tenant staging tables for intelligence feeds.
// Every query requires organization_id.
type OutreachRepository interface {
	// Import runs
	CreateImportRun(ctx context.Context, run *models.OutreachImportRun) error
	UpdateImportRun(ctx context.Context, run *models.OutreachImportRun) error
	GetImportRun(ctx context.Context, orgID, id uuid.UUID) (*models.OutreachImportRun, error)
	GetImportRunByIdempotency(ctx context.Context, orgID uuid.UUID, key string) (*models.OutreachImportRun, error)
	ListImportRuns(ctx context.Context, orgID uuid.UUID, limit int) ([]models.OutreachImportRun, error)

	// Accounts
	GetAccountByCNPJ(ctx context.Context, orgID uuid.UUID, cnpj14 string) (*models.OutreachAccount, error)
	GetAccount(ctx context.Context, orgID, id uuid.UUID) (*models.OutreachAccount, error)
	UpsertAccount(ctx context.Context, acc *models.OutreachAccount) (created bool, err error)
	ListAccounts(ctx context.Context, orgID uuid.UUID, filter OutreachAccountFilter) ([]models.OutreachAccount, error)
	CountByQueueState(ctx context.Context, orgID uuid.UUID) (*models.OutreachQueueSummary, error)
	CountByActivationState(ctx context.Context, orgID uuid.UUID, now time.Time) (*OutreachActivationCounts, error)
	SetAccountHumanFlags(ctx context.Context, orgID, id uuid.UUID, blocked, dnc bool, reason, queueState string) error
	InvalidateAccountOutboundForTargetFit(ctx context.Context, orgID, accountID uuid.UUID, reason string) (TargetFitInvalidationCounts, error)
	// Feed sync durable state (single-flight across replicas via advisory lock + table).
	GetFeedSyncState(ctx context.Context, orgID uuid.UUID) (*models.OutreachFeedSyncState, error)
	UpsertFeedSyncState(ctx context.Context, st *models.OutreachFeedSyncState) error
	TryAdvisoryLock(ctx context.Context, key int64) (bool, error)
	AdvisoryUnlock(ctx context.Context, key int64) error

	// Contacts
	ListCandidates(ctx context.Context, orgID, accountID uuid.UUID) ([]models.OutreachContactCandidate, error)
	UpsertCandidate(ctx context.Context, c *models.OutreachContactCandidate) (created bool, err error)
	GetCandidate(ctx context.Context, orgID, id uuid.UUID) (*models.OutreachContactCandidate, error)

	// Evidence
	ListEvidence(ctx context.Context, orgID, accountID uuid.UUID) ([]models.OutreachEvidence, error)
	UpsertEvidence(ctx context.Context, e *models.OutreachEvidence) (created bool, err error)

	// Drafts + org settings (review / enrollment)
	UpsertDraft(ctx context.Context, d *models.OutreachDraft) error
	GetDraft(ctx context.Context, orgID, id uuid.UUID) (*models.OutreachDraft, error)
	GetActiveDraftForAccount(ctx context.Context, orgID, accountID uuid.UUID) (*models.OutreachDraft, error)
	ListDrafts(ctx context.Context, orgID uuid.UUID, status string, limit, offset int) ([]models.OutreachDraft, error)
	UpdateDraftStatus(ctx context.Context, d *models.OutreachDraft) error
	GetOrgSettings(ctx context.Context, orgID uuid.UUID) (*models.OutreachOrgSettings, error)
	UpsertOrgSettings(ctx context.Context, s *models.OutreachOrgSettings) error

	// Outcome outbox
	EnqueueOutcome(ctx context.Context, ev *models.OutreachOutcome) error
	ListPendingOutcomes(ctx context.Context, limit int) ([]models.OutreachOutcome, error)
	MarkOutcomeDelivered(ctx context.Context, orgID, id uuid.UUID) error
	MarkOutcomeAttempt(ctx context.Context, orgID, id uuid.UUID, attempts int, next time.Time, lastErr string, dead bool) error
	GetOutcomeByIdempotency(ctx context.Context, orgID uuid.UUID, key string) (*models.OutreachOutcome, error)
	FindCandidateByEmail(ctx context.Context, orgID uuid.UUID, email string) (*models.OutreachContactCandidate, *models.OutreachAccount, error)
	FindCandidateByEnrollment(ctx context.Context, orgID, campaignID, contactID uuid.UUID) (*models.OutreachContactCandidate, *models.OutreachAccount, error)
	FindCandidateByPhone(ctx context.Context, orgID uuid.UUID, phone string) (*models.OutreachContactCandidate, *models.OutreachAccount, error)

	InsertTouchpoint(ctx context.Context, t *models.OutreachTouchpoint) error
	UpdateTouchpoint(ctx context.Context, t *models.OutreachTouchpoint) error
	GetTouchpoint(ctx context.Context, orgID, id uuid.UUID) (*models.OutreachTouchpoint, error)
	GetTouchpointByIdempotency(ctx context.Context, orgID uuid.UUID, key string) (*models.OutreachTouchpoint, error)
	GetTouchpointByDraft(ctx context.Context, orgID, draftID uuid.UUID) (*models.OutreachTouchpoint, error)
	ListTouchpoints(ctx context.Context, orgID, accountID uuid.UUID, state string, limit, offset int) ([]models.OutreachTouchpoint, error)
	ListReviewTouchpoints(ctx context.Context, orgID uuid.UUID, limit, offset int) ([]models.OutreachTouchpoint, error)
	CASQueueTouchpoint(ctx context.Context, orgID, id uuid.UUID, expectedContentHash string) (*models.OutreachTouchpoint, error)
	CancelOpenTouchpoints(ctx context.Context, orgID, accountID uuid.UUID, terminalState, stopReason string) (int, error)
	// ListDuePlannedTouchpoints returns PLANNED rows with due_at <= now (caller filters prior release).
	ListDuePlannedTouchpoints(ctx context.Context, orgID uuid.UUID, now time.Time, limit int) ([]models.OutreachTouchpoint, error)

	// Org owner for system-created CRM tasks when inbound has no human actor.
	GetOrgOwnerUserID(ctx context.Context, orgID uuid.UUID) (uuid.UUID, error)
	// Latest handoff/outcome projection for cockpit confidence/snippet/thread.
	GetLatestOutcomeForLead(ctx context.Context, orgID uuid.UUID, cnpj14, sourceLeadID, contactEmail string) (*models.OutreachOutcome, error)
}

// OutreachAccountFilter filters staged accounts.
type OutreachAccountFilter struct {
	QueueState      string
	CNPJ14          string
	Search          string
	Limit           int
	Offset          int
	DynamicPriority bool
	ActivationState string
	// ActivationDueNow: next_best_action_at IS NULL OR next_best_action_at <= now.
	ActivationDueNow bool
	// ActivationNotExpired: activation_expires_at IS NULL OR activation_expires_at > now.
	ActivationNotExpired bool
	// Exclude dominant human queue states from outbound lanes.
	ExcludeTerminal          bool
	RequireTargetFitEligible bool
	RequireOperational       bool
	StableOrder              bool
}

// OutreachActivationCounts is aggregate activation_state distribution for an org.
type OutreachActivationCounts struct {
	Total            int
	Watch            int
	ResearchRequired int
	ActionableNow    int
	Suppressed       int
	// ActionableDueNow is ACTIONABLE_NOW with NBA due and not expired.
	ActionableDueNow int
	NeedsContactDue  int
}

type TargetFitInvalidationCounts struct {
	Touchpoints   int `json:"cancelled_touchpoints"`
	Drafts        int `json:"blocked_drafts"`
	Enrollments   int `json:"detached_enrollments"`
	DispatchItems int `json:"cancelled_dispatch_items"`
}

type outreachRepository struct {
	db *pgxpool.Pool
}

// NewOutreachRepository constructs the Postgres-backed outreach staging repo.
func NewOutreachRepository(db *pgxpool.Pool) OutreachRepository {
	return &outreachRepository{db: db}
}

func (r *outreachRepository) CreateImportRun(ctx context.Context, run *models.OutreachImportRun) error {
	if run.ID == uuid.Nil {
		run.ID = uuid.New()
	}
	now := time.Now().UTC()
	run.CreatedAt = now
	run.UpdatedAt = now
	if run.StartedAt.IsZero() {
		run.StartedAt = now
	}
	counts, _ := json.Marshal(run.Counts)
	errs, _ := json.Marshal(run.Errors)
	if errs == nil {
		errs = []byte("[]")
	}
	warns, _ := json.Marshal(run.Warnings)
	if warns == nil {
		warns = []byte("[]")
	}
	_, err := r.db.Exec(ctx, `
		INSERT INTO outreach_import_runs (
			id, organization_id, source_system, source_run_id, schema_version,
			snapshot_hash, repo_sha, payload_hash, profile_id, profile_version,
			status, dry_run, started_at, finished_at, cursor_in, cursor_out,
			counts, errors, warnings, created_by_user_id, idempotency_key, source_uri,
			created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,
			$6,$7,$8,$9,$10,
			$11,$12,$13,$14,$15,$16,
			$17,$18,$19,$20,$21,$22,
			$23,$23
		)`,
		run.ID, run.OrganizationID, run.SourceSystem, run.SourceRunID, run.SchemaVersion,
		run.SnapshotHash, run.RepoSHA, run.PayloadHash, run.ProfileID, run.ProfileVersion,
		run.Status, run.DryRun, run.StartedAt, run.FinishedAt, run.CursorIn, run.CursorOut,
		counts, errs, warns, run.CreatedByUserID, run.IdempotencyKey, run.SourceURI,
		now,
	)
	return err
}

func (r *outreachRepository) UpdateImportRun(ctx context.Context, run *models.OutreachImportRun) error {
	run.UpdatedAt = time.Now().UTC()
	counts, _ := json.Marshal(run.Counts)
	errs, _ := json.Marshal(run.Errors)
	if errs == nil {
		errs = []byte("[]")
	}
	warns, _ := json.Marshal(run.Warnings)
	if warns == nil {
		warns = []byte("[]")
	}
	ct, err := r.db.Exec(ctx, `
		UPDATE outreach_import_runs SET
			status=$3, finished_at=$4, cursor_out=$5,
			counts=$6, errors=$7, warnings=$8, updated_at=$9
		WHERE id=$1 AND organization_id=$2`,
		run.ID, run.OrganizationID, run.Status, run.FinishedAt, run.CursorOut,
		counts, errs, warns, run.UpdatedAt,
	)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (r *outreachRepository) GetImportRun(ctx context.Context, orgID, id uuid.UUID) (*models.OutreachImportRun, error) {
	row := r.db.QueryRow(ctx, outreachImportRunSelect+`
		FROM outreach_import_runs WHERE id=$1 AND organization_id=$2`, id, orgID)
	return scanImportRun(row)
}

func (r *outreachRepository) GetImportRunByIdempotency(ctx context.Context, orgID uuid.UUID, key string) (*models.OutreachImportRun, error) {
	if key == "" {
		return nil, nil
	}
	row := r.db.QueryRow(ctx, outreachImportRunSelect+`
		FROM outreach_import_runs WHERE organization_id=$1 AND idempotency_key=$2`, orgID, key)
	run, err := scanImportRun(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return run, err
}

func (r *outreachRepository) ListImportRuns(ctx context.Context, orgID uuid.UUID, limit int) ([]models.OutreachImportRun, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.db.Query(ctx, outreachImportRunSelect+`
		FROM outreach_import_runs WHERE organization_id=$1
		ORDER BY started_at DESC LIMIT $2`, orgID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.OutreachImportRun
	for rows.Next() {
		run, err := scanImportRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *run)
	}
	return out, rows.Err()
}

const outreachImportRunSelect = `
	SELECT id, organization_id, source_system, source_run_id, schema_version,
		snapshot_hash, repo_sha, payload_hash, profile_id, profile_version,
		status, dry_run, started_at, finished_at, COALESCE(cursor_in,''), COALESCE(cursor_out,''),
		counts, errors, warnings, created_by_user_id, COALESCE(idempotency_key,''), COALESCE(source_uri,''),
		created_at, updated_at `

type scannable interface {
	Scan(dest ...any) error
}

func scanImportRun(row scannable) (*models.OutreachImportRun, error) {
	var run models.OutreachImportRun
	var counts, errs, warns []byte
	err := row.Scan(
		&run.ID, &run.OrganizationID, &run.SourceSystem, &run.SourceRunID, &run.SchemaVersion,
		&run.SnapshotHash, &run.RepoSHA, &run.PayloadHash, &run.ProfileID, &run.ProfileVersion,
		&run.Status, &run.DryRun, &run.StartedAt, &run.FinishedAt, &run.CursorIn, &run.CursorOut,
		&counts, &errs, &warns, &run.CreatedByUserID, &run.IdempotencyKey, &run.SourceURI,
		&run.CreatedAt, &run.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(counts, &run.Counts)
	_ = json.Unmarshal(errs, &run.Errors)
	_ = json.Unmarshal(warns, &run.Warnings)
	return &run, nil
}

func (r *outreachRepository) GetAccountByCNPJ(ctx context.Context, orgID uuid.UUID, cnpj14 string) (*models.OutreachAccount, error) {
	row := r.db.QueryRow(ctx, outreachAccountSelect+`
		FROM outreach_accounts WHERE organization_id=$1 AND cnpj14=$2`, orgID, cnpj14)
	acc, err := scanAccount(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return acc, err
}

func (r *outreachRepository) GetAccount(ctx context.Context, orgID, id uuid.UUID) (*models.OutreachAccount, error) {
	row := r.db.QueryRow(ctx, outreachAccountSelect+`
		FROM outreach_accounts WHERE organization_id=$1 AND id=$2`, orgID, id)
	acc, err := scanAccount(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return acc, err
}

const outreachAccountSelect = `
	SELECT id, organization_id, COALESCE(source_lead_id,''), cnpj14, COALESCE(cnpj_root,''),
		COALESCE(razao_social,''), COALESCE(nome_fantasia,''), COALESCE(municipio,''), COALESCE(uf,''), COALESCE(website,''),
		priority_rank, priority_score, COALESCE(priority_tier,''), COALESCE(priority_confidence,''),
		COALESCE(moment_code,''), COALESCE(moment_summary,''), moment_observed_at, COALESCE(moment_confidence,''), moment_evidence_ids,
		COALESCE(service_code,''), COALESCE(service_name,''), COALESCE(entry_offer,''), COALESCE(offer_rationale,''),
		COALESCE(fact_to_mention,''), COALESCE(question_to_ask,''), COALESCE(cta,''), claims_to_avoid,
		COALESCE(commercial_state,''), queue_state, human_override, blocked, COALESCE(block_reason,''), do_not_contact,
		COALESCE(source_system,''), COALESCE(source_run_id,''), last_import_run_id, COALESCE(last_payload_hash,''),
		contracts_json, created_at, updated_at,
		COALESCE(activation_state,''), activation_score, activation_reason_codes,
		COALESCE(activation_policy_version,''), activation_evaluated_at, next_best_action_at,
		activation_expires_at, COALESCE(activation_source_hash,''), COALESCE(message_context_hash,''),
		score_components,
		COALESCE(target_fit_send_tier,''), target_fit_reasons, COALESCE(email_send_ready,false),
		COALESCE(target_fit_class,''), target_fit_confidence, COALESCE(target_fit_version,''),
		target_fit_computed_at, COALESCE(target_fit_source_watermark,''), target_fit_observed_at,
		COALESCE(target_fit_fresh,false), target_fit_evidence_ids, COALESCE(target_fit_freshness_reason,''),
		COALESCE(target_fit_eligible,false), COALESCE(target_fit_suppression_reason,''), target_fit_reconciled_at `

func scanAccount(row scannable) (*models.OutreachAccount, error) {
	var a models.OutreachAccount
	var momentEvid, claims []byte
	var contracts []byte
	var reasonCodes, scoreComp, fitReasons, fitEvidence []byte
	err := row.Scan(
		&a.ID, &a.OrganizationID, &a.SourceLeadID, &a.CNPJ14, &a.CNPJRoot,
		&a.RazaoSocial, &a.NomeFantasia, &a.Municipio, &a.UF, &a.Website,
		&a.PriorityRank, &a.PriorityScore, &a.PriorityTier, &a.PriorityConfidence,
		&a.MomentCode, &a.MomentSummary, &a.MomentObservedAt, &a.MomentConfidence, &momentEvid,
		&a.ServiceCode, &a.ServiceName, &a.EntryOffer, &a.OfferRationale,
		&a.FactToMention, &a.QuestionToAsk, &a.CTA, &claims,
		&a.CommercialState, &a.QueueState, &a.HumanOverride, &a.Blocked, &a.BlockReason, &a.DoNotContact,
		&a.SourceSystem, &a.SourceRunID, &a.LastImportRunID, &a.LastPayloadHash,
		&contracts, &a.CreatedAt, &a.UpdatedAt,
		&a.ActivationState, &a.ActivationScore, &reasonCodes,
		&a.ActivationPolicyVersion, &a.ActivationEvaluatedAt, &a.NextBestActionAt,
		&a.ActivationExpiresAt, &a.ActivationSourceHash, &a.MessageContextHash,
		&scoreComp,
		&a.TargetFitSendTier, &fitReasons, &a.EmailSendReady,
		&a.TargetFitClass, &a.TargetFitConfidence, &a.TargetFitVersion,
		&a.TargetFitComputedAt, &a.TargetFitSourceWatermark, &a.TargetFitObservedAt,
		&a.TargetFitFresh, &fitEvidence, &a.TargetFitFreshnessReason,
		&a.TargetFitEligible, &a.TargetFitSuppressionReason, &a.TargetFitReconciledAt,
	)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(momentEvid, &a.MomentEvidenceIDs)
	_ = json.Unmarshal(claims, &a.ClaimsToAvoid)
	_ = json.Unmarshal(reasonCodes, &a.ActivationReasonCodes)
	_ = json.Unmarshal(fitReasons, &a.TargetFitReasons)
	_ = json.Unmarshal(fitEvidence, &a.TargetFitEvidenceIDs)
	a.ContractsJSON = contracts
	a.ScoreComponentsJSON = scoreComp
	return &a, nil
}

func (r *outreachRepository) UpsertAccount(ctx context.Context, acc *models.OutreachAccount) (bool, error) {
	if acc.ID == uuid.Nil {
		acc.ID = uuid.New()
	}
	now := time.Now().UTC()
	acc.UpdatedAt = now
	if acc.CreatedAt.IsZero() {
		acc.CreatedAt = now
	}
	momentEvid, _ := json.Marshal(acc.MomentEvidenceIDs)
	if momentEvid == nil {
		momentEvid = []byte("[]")
	}
	claims, _ := json.Marshal(acc.ClaimsToAvoid)
	if claims == nil {
		claims = []byte("[]")
	}
	contracts := acc.ContractsJSON
	if len(contracts) == 0 {
		contracts = []byte("[]")
	}
	reasonCodes, _ := json.Marshal(acc.ActivationReasonCodes)
	if reasonCodes == nil {
		reasonCodes = []byte("[]")
	}
	scoreComp := acc.ScoreComponentsJSON
	if len(scoreComp) == 0 {
		scoreComp = []byte("{}")
	}
	fitReasons, _ := json.Marshal(acc.TargetFitReasons)
	if fitReasons == nil {
		fitReasons = []byte("[]")
	}
	fitEvidence, _ := json.Marshal(acc.TargetFitEvidenceIDs)
	if fitEvidence == nil {
		fitEvidence = []byte("[]")
	}
	// Machine fields update; human_override / blocked / dnc preserved when set on existing.
	var created bool
	err := r.db.QueryRow(ctx, `
		INSERT INTO outreach_accounts (
			id, organization_id, source_lead_id, cnpj14, cnpj_root,
			razao_social, nome_fantasia, municipio, uf, website,
			priority_rank, priority_score, priority_tier, priority_confidence,
			moment_code, moment_summary, moment_observed_at, moment_confidence, moment_evidence_ids,
			service_code, service_name, entry_offer, offer_rationale,
			fact_to_mention, question_to_ask, cta, claims_to_avoid,
			commercial_state, queue_state, human_override, blocked, block_reason, do_not_contact,
			source_system, source_run_id, last_import_run_id, last_payload_hash, contracts_json,
			created_at, updated_at,
			activation_state, activation_score, activation_reason_codes,
			activation_policy_version, activation_evaluated_at, next_best_action_at,
			activation_expires_at, activation_source_hash, message_context_hash, score_components,
			target_fit_send_tier, target_fit_reasons, email_send_ready,
			target_fit_class, target_fit_confidence, target_fit_version,
			target_fit_computed_at, target_fit_source_watermark, target_fit_observed_at,
			target_fit_fresh, target_fit_evidence_ids, target_fit_freshness_reason,
			target_fit_eligible, target_fit_suppression_reason, target_fit_reconciled_at
		) VALUES (
			$1,$2,$3,$4,$5,
			$6,$7,$8,$9,$10,
			$11,$12,$13,$14,
			$15,$16,$17,$18,$19,
			$20,$21,$22,$23,
			$24,$25,$26,$27,
			$28,$29,$30,$31,$32,$33,
			$34,$35,$36,$37,$38,
			$39,$40,
			$41,$42,$43,
			$44,$45,$46,
			$47,$48,$49,$50,
			$51,$52,$53,
			$54,$55,$56,$57,$58,$59,$60,$61,$62,$63,$64,$65
		)
		ON CONFLICT (organization_id, cnpj14) DO UPDATE SET
			source_lead_id = EXCLUDED.source_lead_id,
			cnpj_root = EXCLUDED.cnpj_root,
			razao_social = CASE WHEN outreach_accounts.human_override THEN outreach_accounts.razao_social ELSE EXCLUDED.razao_social END,
			nome_fantasia = CASE WHEN outreach_accounts.human_override THEN outreach_accounts.nome_fantasia ELSE EXCLUDED.nome_fantasia END,
			municipio = EXCLUDED.municipio,
			uf = EXCLUDED.uf,
			website = EXCLUDED.website,
			priority_rank = EXCLUDED.priority_rank,
			priority_score = EXCLUDED.priority_score,
			priority_tier = EXCLUDED.priority_tier,
			priority_confidence = EXCLUDED.priority_confidence,
			moment_code = EXCLUDED.moment_code,
			moment_summary = EXCLUDED.moment_summary,
			moment_observed_at = EXCLUDED.moment_observed_at,
			moment_confidence = EXCLUDED.moment_confidence,
			moment_evidence_ids = EXCLUDED.moment_evidence_ids,
			service_code = EXCLUDED.service_code,
			service_name = EXCLUDED.service_name,
			entry_offer = EXCLUDED.entry_offer,
			offer_rationale = EXCLUDED.offer_rationale,
			fact_to_mention = EXCLUDED.fact_to_mention,
			question_to_ask = EXCLUDED.question_to_ask,
			cta = EXCLUDED.cta,
			claims_to_avoid = EXCLUDED.claims_to_avoid,
			commercial_state = EXCLUDED.commercial_state,
			queue_state = CASE WHEN
				EXCLUDED.target_fit_observed_at > outreach_accounts.target_fit_observed_at OR
				(outreach_accounts.target_fit_observed_at IS NULL AND EXCLUDED.target_fit_observed_at IS NOT NULL) OR
				(EXCLUDED.target_fit_observed_at = outreach_accounts.target_fit_observed_at AND
				 (NOT EXCLUDED.target_fit_eligible OR outreach_accounts.target_fit_eligible)) OR
				(EXCLUDED.target_fit_observed_at IS NULL AND outreach_accounts.target_fit_observed_at IS NULL AND
				 NOT EXCLUDED.target_fit_eligible)
				THEN EXCLUDED.queue_state ELSE outreach_accounts.queue_state END,
			-- never clear human DNC / block via machine reimport
			blocked = outreach_accounts.blocked OR EXCLUDED.blocked,
			block_reason = CASE WHEN outreach_accounts.blocked THEN outreach_accounts.block_reason ELSE EXCLUDED.block_reason END,
			do_not_contact = outreach_accounts.do_not_contact OR EXCLUDED.do_not_contact,
			source_system = EXCLUDED.source_system,
			source_run_id = EXCLUDED.source_run_id,
			last_import_run_id = EXCLUDED.last_import_run_id,
			last_payload_hash = EXCLUDED.last_payload_hash,
			contracts_json = EXCLUDED.contracts_json,
			activation_state = EXCLUDED.activation_state,
			activation_score = EXCLUDED.activation_score,
			activation_reason_codes = EXCLUDED.activation_reason_codes,
			activation_policy_version = EXCLUDED.activation_policy_version,
			activation_evaluated_at = EXCLUDED.activation_evaluated_at,
			next_best_action_at = EXCLUDED.next_best_action_at,
			activation_expires_at = EXCLUDED.activation_expires_at,
			activation_source_hash = EXCLUDED.activation_source_hash,
			message_context_hash = EXCLUDED.message_context_hash,
			score_components = EXCLUDED.score_components,
			target_fit_send_tier = CASE WHEN
				EXCLUDED.target_fit_observed_at > outreach_accounts.target_fit_observed_at OR
				(outreach_accounts.target_fit_observed_at IS NULL AND EXCLUDED.target_fit_observed_at IS NOT NULL) OR
				(EXCLUDED.target_fit_observed_at = outreach_accounts.target_fit_observed_at AND
				 (NOT EXCLUDED.target_fit_eligible OR outreach_accounts.target_fit_eligible)) OR
				(EXCLUDED.target_fit_observed_at IS NULL AND outreach_accounts.target_fit_observed_at IS NULL AND
				 NOT EXCLUDED.target_fit_eligible)
				THEN EXCLUDED.target_fit_send_tier ELSE outreach_accounts.target_fit_send_tier END,
			target_fit_reasons = CASE WHEN
				EXCLUDED.target_fit_observed_at > outreach_accounts.target_fit_observed_at OR
				(outreach_accounts.target_fit_observed_at IS NULL AND EXCLUDED.target_fit_observed_at IS NOT NULL) OR
				(EXCLUDED.target_fit_observed_at = outreach_accounts.target_fit_observed_at AND (NOT EXCLUDED.target_fit_eligible OR outreach_accounts.target_fit_eligible)) OR
				(EXCLUDED.target_fit_observed_at IS NULL AND outreach_accounts.target_fit_observed_at IS NULL AND NOT EXCLUDED.target_fit_eligible)
				THEN EXCLUDED.target_fit_reasons ELSE outreach_accounts.target_fit_reasons END,
			email_send_ready = CASE WHEN
				EXCLUDED.target_fit_observed_at > outreach_accounts.target_fit_observed_at OR
				(outreach_accounts.target_fit_observed_at IS NULL AND EXCLUDED.target_fit_observed_at IS NOT NULL) OR
				(EXCLUDED.target_fit_observed_at = outreach_accounts.target_fit_observed_at AND (NOT EXCLUDED.target_fit_eligible OR outreach_accounts.target_fit_eligible)) OR
				(EXCLUDED.target_fit_observed_at IS NULL AND outreach_accounts.target_fit_observed_at IS NULL AND NOT EXCLUDED.target_fit_eligible)
				THEN EXCLUDED.email_send_ready ELSE outreach_accounts.email_send_ready END,
			target_fit_class = CASE WHEN
				EXCLUDED.target_fit_observed_at > outreach_accounts.target_fit_observed_at OR
				(outreach_accounts.target_fit_observed_at IS NULL AND EXCLUDED.target_fit_observed_at IS NOT NULL) OR
				(EXCLUDED.target_fit_observed_at = outreach_accounts.target_fit_observed_at AND (NOT EXCLUDED.target_fit_eligible OR outreach_accounts.target_fit_eligible)) OR
				(EXCLUDED.target_fit_observed_at IS NULL AND outreach_accounts.target_fit_observed_at IS NULL AND NOT EXCLUDED.target_fit_eligible)
				THEN EXCLUDED.target_fit_class ELSE outreach_accounts.target_fit_class END,
			target_fit_confidence = CASE WHEN
				EXCLUDED.target_fit_observed_at > outreach_accounts.target_fit_observed_at OR
				(outreach_accounts.target_fit_observed_at IS NULL AND EXCLUDED.target_fit_observed_at IS NOT NULL) OR
				(EXCLUDED.target_fit_observed_at = outreach_accounts.target_fit_observed_at AND (NOT EXCLUDED.target_fit_eligible OR outreach_accounts.target_fit_eligible)) OR
				(EXCLUDED.target_fit_observed_at IS NULL AND outreach_accounts.target_fit_observed_at IS NULL AND NOT EXCLUDED.target_fit_eligible)
				THEN EXCLUDED.target_fit_confidence ELSE outreach_accounts.target_fit_confidence END,
			target_fit_version = CASE WHEN
				EXCLUDED.target_fit_observed_at > outreach_accounts.target_fit_observed_at OR
				(outreach_accounts.target_fit_observed_at IS NULL AND EXCLUDED.target_fit_observed_at IS NOT NULL) OR
				(EXCLUDED.target_fit_observed_at = outreach_accounts.target_fit_observed_at AND (NOT EXCLUDED.target_fit_eligible OR outreach_accounts.target_fit_eligible)) OR
				(EXCLUDED.target_fit_observed_at IS NULL AND outreach_accounts.target_fit_observed_at IS NULL AND NOT EXCLUDED.target_fit_eligible)
				THEN EXCLUDED.target_fit_version ELSE outreach_accounts.target_fit_version END,
			target_fit_computed_at = CASE WHEN
				EXCLUDED.target_fit_observed_at > outreach_accounts.target_fit_observed_at OR
				(outreach_accounts.target_fit_observed_at IS NULL AND EXCLUDED.target_fit_observed_at IS NOT NULL) OR
				(EXCLUDED.target_fit_observed_at = outreach_accounts.target_fit_observed_at AND (NOT EXCLUDED.target_fit_eligible OR outreach_accounts.target_fit_eligible)) OR
				(EXCLUDED.target_fit_observed_at IS NULL AND outreach_accounts.target_fit_observed_at IS NULL AND NOT EXCLUDED.target_fit_eligible)
				THEN EXCLUDED.target_fit_computed_at ELSE outreach_accounts.target_fit_computed_at END,
			target_fit_source_watermark = CASE WHEN
				EXCLUDED.target_fit_observed_at > outreach_accounts.target_fit_observed_at OR
				(outreach_accounts.target_fit_observed_at IS NULL AND EXCLUDED.target_fit_observed_at IS NOT NULL) OR
				(EXCLUDED.target_fit_observed_at = outreach_accounts.target_fit_observed_at AND (NOT EXCLUDED.target_fit_eligible OR outreach_accounts.target_fit_eligible)) OR
				(EXCLUDED.target_fit_observed_at IS NULL AND outreach_accounts.target_fit_observed_at IS NULL AND NOT EXCLUDED.target_fit_eligible)
				THEN EXCLUDED.target_fit_source_watermark ELSE outreach_accounts.target_fit_source_watermark END,
			target_fit_observed_at = CASE WHEN
				EXCLUDED.target_fit_observed_at > outreach_accounts.target_fit_observed_at OR
				(outreach_accounts.target_fit_observed_at IS NULL AND EXCLUDED.target_fit_observed_at IS NOT NULL) OR
				(EXCLUDED.target_fit_observed_at = outreach_accounts.target_fit_observed_at AND (NOT EXCLUDED.target_fit_eligible OR outreach_accounts.target_fit_eligible)) OR
				(EXCLUDED.target_fit_observed_at IS NULL AND outreach_accounts.target_fit_observed_at IS NULL AND NOT EXCLUDED.target_fit_eligible)
				THEN EXCLUDED.target_fit_observed_at ELSE outreach_accounts.target_fit_observed_at END,
			target_fit_fresh = CASE WHEN
				EXCLUDED.target_fit_observed_at > outreach_accounts.target_fit_observed_at OR
				(outreach_accounts.target_fit_observed_at IS NULL AND EXCLUDED.target_fit_observed_at IS NOT NULL) OR
				(EXCLUDED.target_fit_observed_at = outreach_accounts.target_fit_observed_at AND (NOT EXCLUDED.target_fit_eligible OR outreach_accounts.target_fit_eligible)) OR
				(EXCLUDED.target_fit_observed_at IS NULL AND outreach_accounts.target_fit_observed_at IS NULL AND NOT EXCLUDED.target_fit_eligible)
				THEN EXCLUDED.target_fit_fresh ELSE outreach_accounts.target_fit_fresh END,
			target_fit_evidence_ids = CASE WHEN
				EXCLUDED.target_fit_observed_at > outreach_accounts.target_fit_observed_at OR
				(outreach_accounts.target_fit_observed_at IS NULL AND EXCLUDED.target_fit_observed_at IS NOT NULL) OR
				(EXCLUDED.target_fit_observed_at = outreach_accounts.target_fit_observed_at AND (NOT EXCLUDED.target_fit_eligible OR outreach_accounts.target_fit_eligible)) OR
				(EXCLUDED.target_fit_observed_at IS NULL AND outreach_accounts.target_fit_observed_at IS NULL AND NOT EXCLUDED.target_fit_eligible)
				THEN EXCLUDED.target_fit_evidence_ids ELSE outreach_accounts.target_fit_evidence_ids END,
			target_fit_freshness_reason = CASE WHEN
				EXCLUDED.target_fit_observed_at > outreach_accounts.target_fit_observed_at OR
				(outreach_accounts.target_fit_observed_at IS NULL AND EXCLUDED.target_fit_observed_at IS NOT NULL) OR
				(EXCLUDED.target_fit_observed_at = outreach_accounts.target_fit_observed_at AND (NOT EXCLUDED.target_fit_eligible OR outreach_accounts.target_fit_eligible)) OR
				(EXCLUDED.target_fit_observed_at IS NULL AND outreach_accounts.target_fit_observed_at IS NULL AND NOT EXCLUDED.target_fit_eligible)
				THEN EXCLUDED.target_fit_freshness_reason ELSE outreach_accounts.target_fit_freshness_reason END,
			target_fit_eligible = CASE WHEN
				EXCLUDED.target_fit_observed_at > outreach_accounts.target_fit_observed_at OR
				(outreach_accounts.target_fit_observed_at IS NULL AND EXCLUDED.target_fit_observed_at IS NOT NULL) OR
				(EXCLUDED.target_fit_observed_at = outreach_accounts.target_fit_observed_at AND (NOT EXCLUDED.target_fit_eligible OR outreach_accounts.target_fit_eligible)) OR
				(EXCLUDED.target_fit_observed_at IS NULL AND outreach_accounts.target_fit_observed_at IS NULL AND NOT EXCLUDED.target_fit_eligible)
				THEN EXCLUDED.target_fit_eligible ELSE outreach_accounts.target_fit_eligible END,
			target_fit_suppression_reason = CASE WHEN
				EXCLUDED.target_fit_observed_at > outreach_accounts.target_fit_observed_at OR
				(outreach_accounts.target_fit_observed_at IS NULL AND EXCLUDED.target_fit_observed_at IS NOT NULL) OR
				(EXCLUDED.target_fit_observed_at = outreach_accounts.target_fit_observed_at AND (NOT EXCLUDED.target_fit_eligible OR outreach_accounts.target_fit_eligible)) OR
				(EXCLUDED.target_fit_observed_at IS NULL AND outreach_accounts.target_fit_observed_at IS NULL AND NOT EXCLUDED.target_fit_eligible)
				THEN EXCLUDED.target_fit_suppression_reason ELSE outreach_accounts.target_fit_suppression_reason END,
			target_fit_reconciled_at = EXCLUDED.target_fit_reconciled_at,
			updated_at = EXCLUDED.updated_at,
			id = outreach_accounts.id
		RETURNING (xmax = 0) AS inserted, id`,
		acc.ID, acc.OrganizationID, acc.SourceLeadID, acc.CNPJ14, acc.CNPJRoot,
		acc.RazaoSocial, acc.NomeFantasia, acc.Municipio, acc.UF, acc.Website,
		acc.PriorityRank, acc.PriorityScore, acc.PriorityTier, acc.PriorityConfidence,
		acc.MomentCode, acc.MomentSummary, acc.MomentObservedAt, acc.MomentConfidence, momentEvid,
		acc.ServiceCode, acc.ServiceName, acc.EntryOffer, acc.OfferRationale,
		acc.FactToMention, acc.QuestionToAsk, acc.CTA, claims,
		acc.CommercialState, acc.QueueState, acc.HumanOverride, acc.Blocked, acc.BlockReason, acc.DoNotContact,
		acc.SourceSystem, acc.SourceRunID, acc.LastImportRunID, acc.LastPayloadHash, contracts,
		acc.CreatedAt, acc.UpdatedAt,
		acc.ActivationState, acc.ActivationScore, reasonCodes,
		acc.ActivationPolicyVersion, acc.ActivationEvaluatedAt, acc.NextBestActionAt,
		acc.ActivationExpiresAt, acc.ActivationSourceHash, acc.MessageContextHash, scoreComp,
		acc.TargetFitSendTier, fitReasons, acc.EmailSendReady,
		acc.TargetFitClass, acc.TargetFitConfidence, acc.TargetFitVersion,
		acc.TargetFitComputedAt, acc.TargetFitSourceWatermark, acc.TargetFitObservedAt,
		acc.TargetFitFresh, fitEvidence, acc.TargetFitFreshnessReason,
		acc.TargetFitEligible, acc.TargetFitSuppressionReason, acc.TargetFitReconciledAt,
	).Scan(&created, &acc.ID)
	return created, err
}

func (r *outreachRepository) ListAccounts(ctx context.Context, orgID uuid.UUID, filter OutreachAccountFilter) ([]models.OutreachAccount, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 1000 {
		limit = 1000
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	q := outreachAccountSelect + ` FROM outreach_accounts WHERE organization_id=$1`
	args := []any{orgID}
	n := 2
	if filter.QueueState != "" {
		q += fmt.Sprintf(` AND queue_state=$%d`, n)
		args = append(args, filter.QueueState)
		n++
	}
	if filter.CNPJ14 != "" {
		q += fmt.Sprintf(` AND cnpj14=$%d`, n)
		args = append(args, filter.CNPJ14)
		n++
	}
	if filter.Search != "" {
		q += fmt.Sprintf(` AND (razao_social ILIKE $%d OR nome_fantasia ILIKE $%d OR cnpj14 LIKE $%d)`, n, n, n)
		args = append(args, "%"+filter.Search+"%")
		n++
	}
	if filter.ActivationState != "" {
		q += fmt.Sprintf(` AND activation_state=$%d`, n)
		args = append(args, filter.ActivationState)
		n++
	}
	if filter.ActivationDueNow {
		q += ` AND (next_best_action_at IS NULL OR next_best_action_at <= now())`
	}
	if filter.ActivationNotExpired {
		q += ` AND (activation_expires_at IS NULL OR activation_expires_at > now())`
	}
	if filter.ExcludeTerminal {
		q += ` AND queue_state NOT IN ('DO_NOT_CONTACT','BLOCKED','BOUNCED','REPLIED','MEETING','PROPOSAL','WON','LOST','SENT','ENROLLED')`
		q += ` AND do_not_contact = false AND blocked = false`
	}
	if filter.RequireTargetFitEligible || filter.RequireOperational {
		q += ` AND target_fit_eligible = true AND target_fit_class = 'TARGET_CONFIRMED'`
		q += ` AND target_fit_fresh = true AND target_fit_version <> '' AND target_fit_source_watermark <> '' AND target_fit_observed_at IS NOT NULL`
	}
	if filter.RequireOperational {
		q += ` AND email_send_ready = true`
		q += ` AND EXISTS (SELECT 1 FROM outreach_contact_candidates occ WHERE occ.organization_id=outreach_accounts.organization_id AND occ.account_id=outreach_accounts.id AND occ.email_send_ready=true AND occ.email<>'' AND occ.blocked=false AND occ.do_not_contact=false AND occ.bounced=false AND occ.mailbox_purpose_send_blocked=false AND occ.verification_status NOT IN ('CANDIDATE_UNVERIFIED','NOT_FOUND','INVALID','BOUNCED','DO_NOT_CONTACT'))`
	}
	if filter.StableOrder {
		q += fmt.Sprintf(` ORDER BY cnpj14 ASC LIMIT $%d OFFSET $%d`, n, n+1)
	} else if filter.DynamicPriority {
		q += fmt.Sprintf(` ORDER BY next_best_action_at ASC NULLS LAST, activation_score DESC, priority_rank ASC NULLS LAST, moment_observed_at DESC NULLS LAST, cnpj14 ASC LIMIT $%d OFFSET $%d`, n, n+1)
	} else {
		q += fmt.Sprintf(` ORDER BY priority_rank ASC NULLS LAST, updated_at DESC LIMIT $%d OFFSET $%d`, n, n+1)
	}
	args = append(args, limit, offset)

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.OutreachAccount
	for rows.Next() {
		acc, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *acc)
	}
	return out, rows.Err()
}

func (r *outreachRepository) CountByActivationState(ctx context.Context, orgID uuid.UUID, now time.Time) (*OutreachActivationCounts, error) {
	_ = now
	out := &OutreachActivationCounts{}
	rows, err := r.db.Query(ctx, `
		SELECT COALESCE(activation_state,''), COUNT(*)::int
		FROM outreach_accounts WHERE organization_id=$1
		GROUP BY COALESCE(activation_state,'')`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out.Total += n
		switch st {
		case "WATCH":
			out.Watch = n
		case "RESEARCH_REQUIRED":
			out.ResearchRequired = n
		case "ACTIONABLE_NOW":
			out.ActionableNow = n
		case "SUPPRESSED":
			out.Suppressed = n
		default:
			out.Watch += n
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Due ACTIONABLE_NOW (NBA ready, not expired, not terminal)
	err = r.db.QueryRow(ctx, `
		SELECT COUNT(*)::int FROM outreach_accounts
		WHERE organization_id=$1
		  AND activation_state='ACTIONABLE_NOW'
		  AND target_fit_eligible=true AND target_fit_class='TARGET_CONFIRMED'
		  AND target_fit_fresh=true AND target_fit_version<>''
		  AND target_fit_source_watermark<>'' AND target_fit_observed_at IS NOT NULL
		  AND email_send_ready=true
		  AND do_not_contact=false AND blocked=false
		  AND queue_state NOT IN ('DO_NOT_CONTACT','BLOCKED','BOUNCED','REPLIED','MEETING','PROPOSAL','WON','LOST','SENT','ENROLLED')
		  AND (next_best_action_at IS NULL OR next_best_action_at <= now())
		  AND (activation_expires_at IS NULL OR activation_expires_at > now())
		  AND EXISTS (
			SELECT 1 FROM outreach_contact_candidates occ
			WHERE occ.organization_id=outreach_accounts.organization_id
			  AND occ.account_id=outreach_accounts.id AND occ.email_send_ready=true AND occ.email<>''
			  AND occ.blocked=false AND occ.do_not_contact=false AND occ.bounced=false
			  AND occ.mailbox_purpose_send_blocked=false
			  AND occ.verification_status NOT IN ('CANDIDATE_UNVERIFIED','NOT_FOUND','INVALID','BOUNCED','DO_NOT_CONTACT')
		  )`,
		orgID).Scan(&out.ActionableDueNow)
	if err != nil {
		return nil, err
	}
	err = r.db.QueryRow(ctx, `
		SELECT COUNT(*)::int FROM outreach_accounts
		WHERE organization_id=$1
		  AND activation_state='ACTIONABLE_NOW'
		  AND queue_state='NEEDS_CONTACT'
		  AND target_fit_eligible=true AND target_fit_class='TARGET_CONFIRMED'
		  AND target_fit_fresh=true AND target_fit_version<>''
		  AND target_fit_source_watermark<>'' AND target_fit_observed_at IS NOT NULL
		  AND do_not_contact=false AND blocked=false
		  AND (next_best_action_at IS NULL OR next_best_action_at <= now())
		  AND (activation_expires_at IS NULL OR activation_expires_at > now())`,
		orgID).Scan(&out.NeedsContactDue)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *outreachRepository) GetFeedSyncState(ctx context.Context, orgID uuid.UUID) (*models.OutreachFeedSyncState, error) {
	row := r.db.QueryRow(ctx, `
		SELECT organization_id, COALESCE(last_snapshot_hash,''), COALESCE(last_run_id,''),
			COALESCE(last_manifest_uri,''), last_success_at, last_attempt_at,
			COALESCE(last_error,''), COALESCE(last_status,'idle'), counts, updated_at
		FROM outreach_feed_sync_state WHERE organization_id=$1`, orgID)
	var st models.OutreachFeedSyncState
	var counts []byte
	err := row.Scan(
		&st.OrganizationID, &st.LastSnapshotHash, &st.LastRunID,
		&st.LastManifestURI, &st.LastSuccessAt, &st.LastAttemptAt,
		&st.LastError, &st.LastStatus, &counts, &st.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	st.CountsJSON = counts
	return &st, nil
}

func (r *outreachRepository) UpsertFeedSyncState(ctx context.Context, st *models.OutreachFeedSyncState) error {
	if st == nil {
		return errors.New("nil feed sync state")
	}
	now := time.Now().UTC()
	st.UpdatedAt = now
	counts := st.CountsJSON
	if len(counts) == 0 {
		counts = []byte("{}")
	}
	_, err := r.db.Exec(ctx, `
		INSERT INTO outreach_feed_sync_state (
			organization_id, last_snapshot_hash, last_run_id, last_manifest_uri,
			last_success_at, last_attempt_at, last_error, last_status, counts, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (organization_id) DO UPDATE SET
			last_snapshot_hash = EXCLUDED.last_snapshot_hash,
			last_run_id = EXCLUDED.last_run_id,
			last_manifest_uri = EXCLUDED.last_manifest_uri,
			last_success_at = COALESCE(EXCLUDED.last_success_at, outreach_feed_sync_state.last_success_at),
			last_attempt_at = EXCLUDED.last_attempt_at,
			last_error = EXCLUDED.last_error,
			last_status = EXCLUDED.last_status,
			counts = EXCLUDED.counts,
			updated_at = EXCLUDED.updated_at`,
		st.OrganizationID, st.LastSnapshotHash, st.LastRunID, st.LastManifestURI,
		st.LastSuccessAt, st.LastAttemptAt, st.LastError, st.LastStatus, counts, st.UpdatedAt,
	)
	return err
}

func (r *outreachRepository) TryAdvisoryLock(ctx context.Context, key int64) (bool, error) {
	var ok bool
	err := r.db.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&ok)
	return ok, err
}

func (r *outreachRepository) AdvisoryUnlock(ctx context.Context, key int64) error {
	_, err := r.db.Exec(ctx, `SELECT pg_advisory_unlock($1)`, key)
	return err
}

func (r *outreachRepository) CountByQueueState(ctx context.Context, orgID uuid.UUID) (*models.OutreachQueueSummary, error) {
	rows, err := r.db.Query(ctx, `
		SELECT queue_state, COUNT(*)::int
		FROM outreach_accounts WHERE organization_id=$1
		GROUP BY queue_state`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sum := &models.OutreachQueueSummary{}
	for rows.Next() {
		var state string
		var n int
		if err := rows.Scan(&state, &n); err != nil {
			return nil, err
		}
		sum.Total += n
		switch state {
		case models.OutreachQueueNeedsContact:
			sum.NeedsContact = n
		case models.OutreachQueueReadyToGenerate:
			sum.ReadyToGenerate = n
		case models.OutreachQueueNeedsReview:
			sum.NeedsReview = n
		case models.OutreachQueueApproved:
			sum.Approved = n
		case models.OutreachQueueEnrolled:
			sum.Enrolled = n
		case models.OutreachQueueSent:
			sum.Sent = n
		case models.OutreachQueueReplied:
			sum.Replied = n
		case models.OutreachQueueMeeting:
			sum.Meeting = n
		case models.OutreachQueueProposal:
			sum.Proposal = n
		case models.OutreachQueueWon:
			sum.Won = n
		case models.OutreachQueueBlocked:
			sum.Blocked = n
		case models.OutreachQueueBounced:
			sum.Bounced = n
		case models.OutreachQueueDoNotContact:
			sum.DoNotContact = n
		}
	}
	return sum, rows.Err()
}

func (r *outreachRepository) SetAccountHumanFlags(ctx context.Context, orgID, id uuid.UUID, blocked, dnc bool, reason, queueState string) error {
	ct, err := r.db.Exec(ctx, `
		UPDATE outreach_accounts SET
			blocked=$3, do_not_contact=$4, block_reason=$5, queue_state=$6,
			human_override=true, updated_at=now()
		WHERE organization_id=$1 AND id=$2`,
		orgID, id, blocked, dnc, reason, queueState,
	)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (r *outreachRepository) InvalidateAccountOutboundForTargetFit(ctx context.Context, orgID, accountID uuid.UUID, reason string) (TargetFitInvalidationCounts, error) {
	var out TargetFitInvalidationCounts
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	ct, err := tx.Exec(ctx, `
		UPDATE outreach_touchpoints SET state='CANCELLED', stop_reason=$3,
			approved_by=NULL, approved_at=NULL, approved_content_hash='', authorization_mode='',
			campaign_policy_authorization_id=NULL, authorization_policy_hash='', authorization_at=NULL, updated_at=now()
		WHERE organization_id=$1 AND account_id=$2
		  AND state IN ('PLANNED','DUE','DRAFTED','NEEDS_REVIEW','APPROVED','QUEUED')`, orgID, accountID, reason)
	if err != nil {
		return out, err
	}
	out.Touchpoints = int(ct.RowsAffected())
	ct, err = tx.Exec(ctx, `
		UPDATE confenge_dispatch_queue q SET status='cancelled', cancel_reason=$3, updated_at=now()
		FROM outreach_drafts d
		WHERE q.draft_id=d.id AND d.organization_id=$1 AND d.account_id=$2
		  AND q.status IN ('queued','reserved')`, orgID, accountID, reason)
	if err != nil {
		return out, err
	}
	out.DispatchItems = int(ct.RowsAffected())
	ct, err = tx.Exec(ctx, `
		DELETE FROM campaign_leads cl USING outreach_drafts d
		WHERE d.organization_id=$1 AND d.account_id=$2
		  AND cl.campaign_id=d.campaign_id AND cl.contact_id=d.enrollment_contact_id
		  AND d.status IN ('NOT_GENERATED','GENERATING','NEEDS_REVIEW','APPROVED','ENROLLED')`, orgID, accountID)
	if err != nil {
		return out, err
	}
	out.Enrollments = int(ct.RowsAffected())
	ct, err = tx.Exec(ctx, `
		UPDATE outreach_drafts SET status='BLOCKED', updated_at=now()
		WHERE organization_id=$1 AND account_id=$2
		  AND status IN ('NOT_GENERATED','GENERATING','NEEDS_REVIEW','APPROVED','ENROLLED')`, orgID, accountID)
	if err != nil {
		return out, err
	}
	out.Drafts = int(ct.RowsAffected())
	if out.Touchpoints+out.Drafts+out.Enrollments+out.DispatchItems > 0 {
		_, err = tx.Exec(ctx, `
			INSERT INTO outreach_target_fit_reconciliation_events (
				organization_id, account_id, target_fit_class, target_fit_version,
				target_fit_source_watermark, eligible, reason, cancelled_touchpoints,
				blocked_drafts, detached_enrollments, cancelled_dispatch_items)
			SELECT organization_id, id, target_fit_class, target_fit_version,
				target_fit_source_watermark, false, $3, $4, $5, $6, $7
			FROM outreach_accounts WHERE organization_id=$1 AND id=$2`,
			orgID, accountID, reason, out.Touchpoints, out.Drafts, out.Enrollments, out.DispatchItems)
		if err != nil {
			return out, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return out, err
	}
	return out, nil
}

func (r *outreachRepository) ListCandidates(ctx context.Context, orgID, accountID uuid.UUID) ([]models.OutreachContactCandidate, error) {
	rows, err := r.db.Query(ctx, outreachCandidateSelect+`
		FROM outreach_contact_candidates
		WHERE organization_id=$1 AND account_id=$2
		ORDER BY recommended DESC, created_at ASC`, orgID, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.OutreachContactCandidate
	for rows.Next() {
		c, err := scanCandidate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

const outreachCandidateSelect = `
	SELECT id, organization_id, account_id, COALESCE(source_contact_id,''),
		COALESCE(name,''), COALESCE(role,''), COALESCE(email,''), COALESCE(phone,''),
		COALESCE(phone_e164,''), COALESCE(phone_source,''), COALESCE(phone_source_url,''),
		COALESCE(whatsapp_consent_status,'UNKNOWN'), COALESCE(whatsapp_consent_source,''),
		whatsapp_consent_at, COALESCE(whatsapp_consent_provenance_ok,false),
		COALESCE(linkedin_url,''), COALESCE(source_url,''), COALESCE(source_document,''), source_date,
		verification_status, COALESCE(confidence,''), recommended,
		warmbly_contact_id, promoted_at, blocked, COALESCE(block_reason,''), do_not_contact, bounced,
		COALESCE(email_send_ready,false), COALESCE(mailbox_purpose,''), COALESCE(mailbox_purpose_send_blocked,false),
		COALESCE(ownership_status,''), COALESCE(recipient_commercial_suitability,''),
		last_import_run_id, created_at, updated_at `

func scanCandidate(row scannable) (*models.OutreachContactCandidate, error) {
	var c models.OutreachContactCandidate
	err := row.Scan(
		&c.ID, &c.OrganizationID, &c.AccountID, &c.SourceContactID,
		&c.Name, &c.Role, &c.Email, &c.Phone,
		&c.PhoneE164, &c.PhoneSource, &c.PhoneSourceURL,
		&c.WhatsAppConsentStatus, &c.WhatsAppConsentSource,
		&c.WhatsAppConsentAt, &c.WhatsAppConsentProvenanceOK,
		&c.LinkedInURL, &c.SourceURL, &c.SourceDocument, &c.SourceDate,
		&c.VerificationStatus, &c.Confidence, &c.Recommended,
		&c.WarmblyContactID, &c.PromotedAt, &c.Blocked, &c.BlockReason, &c.DoNotContact, &c.Bounced,
		&c.EmailSendReady, &c.MailboxPurpose, &c.MailboxPurposeSendBlocked,
		&c.OwnershipStatus, &c.RecipientCommercialSuitability,
		&c.LastImportRunID, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *outreachRepository) GetCandidate(ctx context.Context, orgID, id uuid.UUID) (*models.OutreachContactCandidate, error) {
	row := r.db.QueryRow(ctx, outreachCandidateSelect+`
		FROM outreach_contact_candidates WHERE organization_id=$1 AND id=$2`, orgID, id)
	c, err := scanCandidate(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

func (r *outreachRepository) UpsertCandidate(ctx context.Context, c *models.OutreachContactCandidate) (bool, error) {
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	now := time.Now().UTC()
	c.UpdatedAt = now
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	// Prefer unique (org, account, source_contact_id) when source id present.
	if c.WhatsAppConsentStatus == "" {
		c.WhatsAppConsentStatus = "UNKNOWN"
	}
	if c.SourceContactID != "" {
		var created bool
		err := r.db.QueryRow(ctx, `
			INSERT INTO outreach_contact_candidates (
				id, organization_id, account_id, source_contact_id,
				name, role, email, phone,
				phone_e164, phone_source, phone_source_url,
				whatsapp_consent_status, whatsapp_consent_source, whatsapp_consent_at, whatsapp_consent_provenance_ok,
				linkedin_url, source_url, source_document, source_date,
				verification_status, confidence, recommended,
				blocked, block_reason, do_not_contact, bounced,
				email_send_ready, mailbox_purpose, mailbox_purpose_send_blocked,
				ownership_status, recipient_commercial_suitability,
				last_import_run_id, created_at, updated_at
			) VALUES (
				$1,$2,$3,$4,
				$5,$6,$7,$8,
				$9,$10,$11,
				$12,$13,$14,$15,
				$16,$17,$18,$19,
				$20,$21,$22,
				$23,$24,$25,$26,
				$27,$28,$29,
				$30,$31,
				$32,$33,$34
			)
			ON CONFLICT (organization_id, account_id, source_contact_id) WHERE source_contact_id <> '' DO UPDATE SET
				name = EXCLUDED.name,
				role = EXCLUDED.role,
				email = CASE WHEN outreach_contact_candidates.do_not_contact OR outreach_contact_candidates.bounced
					THEN outreach_contact_candidates.email ELSE EXCLUDED.email END,
				phone = EXCLUDED.phone,
				phone_e164 = EXCLUDED.phone_e164,
				phone_source = EXCLUDED.phone_source,
				phone_source_url = EXCLUDED.phone_source_url,
				-- sticky opt-out / DNC never softened by import
				whatsapp_consent_status = CASE
					WHEN outreach_contact_candidates.whatsapp_consent_status IN ('OPTED_OUT','DO_NOT_CONTACT')
						THEN outreach_contact_candidates.whatsapp_consent_status
					WHEN outreach_contact_candidates.do_not_contact
						THEN outreach_contact_candidates.whatsapp_consent_status
					ELSE EXCLUDED.whatsapp_consent_status
				END,
				whatsapp_consent_source = CASE
					WHEN outreach_contact_candidates.whatsapp_consent_status IN ('OPTED_OUT','DO_NOT_CONTACT')
						THEN outreach_contact_candidates.whatsapp_consent_source
					ELSE EXCLUDED.whatsapp_consent_source
				END,
				whatsapp_consent_at = CASE
					WHEN outreach_contact_candidates.whatsapp_consent_status IN ('OPTED_OUT','DO_NOT_CONTACT')
						THEN outreach_contact_candidates.whatsapp_consent_at
					ELSE EXCLUDED.whatsapp_consent_at
				END,
				whatsapp_consent_provenance_ok = EXCLUDED.whatsapp_consent_provenance_ok,
				linkedin_url = EXCLUDED.linkedin_url,
				source_url = EXCLUDED.source_url,
				source_document = EXCLUDED.source_document,
				source_date = EXCLUDED.source_date,
				verification_status = CASE WHEN outreach_contact_candidates.do_not_contact OR outreach_contact_candidates.bounced
					THEN outreach_contact_candidates.verification_status ELSE EXCLUDED.verification_status END,
				confidence = EXCLUDED.confidence,
				recommended = EXCLUDED.recommended,
				do_not_contact = outreach_contact_candidates.do_not_contact OR EXCLUDED.do_not_contact,
				bounced = outreach_contact_candidates.bounced OR EXCLUDED.bounced,
				blocked = outreach_contact_candidates.blocked OR EXCLUDED.blocked,
				email_send_ready = EXCLUDED.email_send_ready,
				mailbox_purpose = EXCLUDED.mailbox_purpose,
				mailbox_purpose_send_blocked = EXCLUDED.mailbox_purpose_send_blocked,
				ownership_status = EXCLUDED.ownership_status,
				recipient_commercial_suitability = EXCLUDED.recipient_commercial_suitability,
				last_import_run_id = EXCLUDED.last_import_run_id,
				updated_at = EXCLUDED.updated_at,
				id = outreach_contact_candidates.id
			RETURNING (xmax = 0), id`,
			c.ID, c.OrganizationID, c.AccountID, c.SourceContactID,
			c.Name, c.Role, c.Email, c.Phone,
			c.PhoneE164, c.PhoneSource, c.PhoneSourceURL,
			c.WhatsAppConsentStatus, c.WhatsAppConsentSource, c.WhatsAppConsentAt, c.WhatsAppConsentProvenanceOK,
			c.LinkedInURL, c.SourceURL, c.SourceDocument, c.SourceDate,
			c.VerificationStatus, c.Confidence, c.Recommended,
			c.Blocked, c.BlockReason, c.DoNotContact, c.Bounced,
			c.EmailSendReady, c.MailboxPurpose, c.MailboxPurposeSendBlocked,
			c.OwnershipStatus, c.RecipientCommercialSuitability,
			c.LastImportRunID, c.CreatedAt, c.UpdatedAt,
		).Scan(&created, &c.ID)
		return created, err
	}
	// No source id: insert only (cannot safely dedupe).
	_, err := r.db.Exec(ctx, `
		INSERT INTO outreach_contact_candidates (
			id, organization_id, account_id, source_contact_id,
			name, role, email, phone,
			phone_e164, phone_source, phone_source_url,
			whatsapp_consent_status, whatsapp_consent_source, whatsapp_consent_at, whatsapp_consent_provenance_ok,
			linkedin_url, source_url, source_document, source_date,
			verification_status, confidence, recommended,
			blocked, block_reason, do_not_contact, bounced,
			email_send_ready, mailbox_purpose, mailbox_purpose_send_blocked,
			ownership_status, recipient_commercial_suitability,
			last_import_run_id, created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34
		)`,
		c.ID, c.OrganizationID, c.AccountID, c.SourceContactID,
		c.Name, c.Role, c.Email, c.Phone,
		c.PhoneE164, c.PhoneSource, c.PhoneSourceURL,
		c.WhatsAppConsentStatus, c.WhatsAppConsentSource, c.WhatsAppConsentAt, c.WhatsAppConsentProvenanceOK,
		c.LinkedInURL, c.SourceURL, c.SourceDocument, c.SourceDate,
		c.VerificationStatus, c.Confidence, c.Recommended,
		c.Blocked, c.BlockReason, c.DoNotContact, c.Bounced,
		c.EmailSendReady, c.MailboxPurpose, c.MailboxPurposeSendBlocked,
		c.OwnershipStatus, c.RecipientCommercialSuitability,
		c.LastImportRunID, c.CreatedAt, c.UpdatedAt,
	)
	return true, err
}

func (r *outreachRepository) ListEvidence(ctx context.Context, orgID, accountID uuid.UUID) ([]models.OutreachEvidence, error) {
	rows, err := r.db.Query(ctx, outreachEvidenceSelect+`
		FROM outreach_evidence WHERE organization_id=$1 AND account_id=$2
		ORDER BY evidence_date DESC NULLS LAST, created_at DESC`, orgID, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.OutreachEvidence
	for rows.Next() {
		e, err := scanEvidence(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

const outreachEvidenceSelect = `
	SELECT id, organization_id, account_id, source_evidence_id,
		COALESCE(evidence_type,''), COALESCE(title,''), COALESCE(url,''), COALESCE(document,''),
		evidence_date, COALESCE(location,''), COALESCE(excerpt,''), COALESCE(synthesis,''),
		epistemic_class, COALESCE(reliability,''), consulted_at,
		last_import_run_id, created_at, updated_at `

func scanEvidence(row scannable) (*models.OutreachEvidence, error) {
	var e models.OutreachEvidence
	err := row.Scan(
		&e.ID, &e.OrganizationID, &e.AccountID, &e.SourceEvidenceID,
		&e.EvidenceType, &e.Title, &e.URL, &e.Document,
		&e.EvidenceDate, &e.Location, &e.Excerpt, &e.Synthesis,
		&e.EpistemicClass, &e.Reliability, &e.ConsultedAt,
		&e.LastImportRunID, &e.CreatedAt, &e.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (r *outreachRepository) UpsertEvidence(ctx context.Context, e *models.OutreachEvidence) (bool, error) {
	if e.ID == uuid.Nil {
		e.ID = uuid.New()
	}
	now := time.Now().UTC()
	e.UpdatedAt = now
	if e.CreatedAt.IsZero() {
		e.CreatedAt = now
	}
	var created bool
	err := r.db.QueryRow(ctx, `
		INSERT INTO outreach_evidence (
			id, organization_id, account_id, source_evidence_id,
			evidence_type, title, url, document, evidence_date, location,
			excerpt, synthesis, epistemic_class, reliability, consulted_at,
			last_import_run_id, created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18
		)
		ON CONFLICT (organization_id, account_id, source_evidence_id) DO UPDATE SET
			evidence_type = EXCLUDED.evidence_type,
			title = EXCLUDED.title,
			url = EXCLUDED.url,
			document = EXCLUDED.document,
			evidence_date = EXCLUDED.evidence_date,
			location = EXCLUDED.location,
			excerpt = EXCLUDED.excerpt,
			synthesis = EXCLUDED.synthesis,
			epistemic_class = EXCLUDED.epistemic_class,
			reliability = EXCLUDED.reliability,
			consulted_at = EXCLUDED.consulted_at,
			last_import_run_id = EXCLUDED.last_import_run_id,
			updated_at = EXCLUDED.updated_at,
			id = outreach_evidence.id
		RETURNING (xmax = 0), id`,
		e.ID, e.OrganizationID, e.AccountID, e.SourceEvidenceID,
		e.EvidenceType, e.Title, e.URL, e.Document, e.EvidenceDate, e.Location,
		e.Excerpt, e.Synthesis, e.EpistemicClass, e.Reliability, e.ConsultedAt,
		e.LastImportRunID, e.CreatedAt, e.UpdatedAt,
	).Scan(&created, &e.ID)
	return created, err
}
