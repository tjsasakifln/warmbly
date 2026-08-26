package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/warmbly/warmbly/internal/models"
)

const outreachTouchpointSelect = `
	SELECT t.id, t.organization_id, t.account_id, t.contact_candidate_id,
		t.ordinal, COALESCE(t.cadence_step,''), COALESCE(t.channel,'EMAIL'), COALESCE(t.purpose,''),
		t.due_at, t.state, t.draft_id,
		COALESCE(t.recipient,''), COALESCE(t.subject,''), COALESCE(t.body_text,''),
		COALESCE(t.content_hash,''), COALESCE(t.approved_content_hash,''), t.approved_by, t.approved_at,
		COALESCE(t.authorization_mode,''),
		t.campaign_policy_authorization_id, COALESCE(t.authorization_policy_hash,''), t.authorization_at,
		COALESCE(t.signature_version,''),
		t.queued_at, t.sent_at, COALESCE(t.provider_message_id,''), COALESCE(t.stop_reason,''),
		t.previous_touchpoint_id, COALESCE(t.idempotency_key,''),
		COALESCE(t.policy_version,''), COALESCE(t.service_code,''), COALESCE(t.fact_used,''), t.evidence_ids,
		COALESCE(t.generated_context_hash,''), COALESCE(t.source_run_id,''),
		t.created_at, t.updated_at `

func scanTouchpoint(row scannable) (*models.OutreachTouchpoint, error) {
	var t models.OutreachTouchpoint
	var evid []byte
	err := row.Scan(
		&t.ID, &t.OrganizationID, &t.AccountID, &t.ContactCandidateID,
		&t.Ordinal, &t.CadenceStep, &t.Channel, &t.Purpose,
		&t.DueAt, &t.State, &t.DraftID,
		&t.Recipient, &t.Subject, &t.BodyText,
		&t.ContentHash, &t.ApprovedContentHash, &t.ApprovedBy, &t.ApprovedAt,
		&t.AuthorizationMode,
		&t.CampaignPolicyAuthorizationID, &t.AuthorizationPolicyHash, &t.AuthorizationAt,
		&t.SignatureVersion,
		&t.QueuedAt, &t.SentAt, &t.ProviderMessageID, &t.StopReason,
		&t.PreviousTouchpointID, &t.IdempotencyKey,
		&t.PolicyVersion, &t.ServiceCode, &t.FactUsed, &evid,
		&t.GeneratedContextHash, &t.SourceRunID,
		&t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(evid, &t.EvidenceIDs)
	return &t, nil
}

func (r *outreachRepository) InsertTouchpoint(ctx context.Context, t *models.OutreachTouchpoint) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	now := time.Now().UTC()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	if t.DueAt.IsZero() {
		t.DueAt = now
	}
	if t.Channel == "" {
		t.Channel = models.OutreachChannelEmail
	}
	if t.PolicyVersion == "" {
		t.PolicyVersion = models.CadencePolicyVersionV1
	}
	evid, _ := json.Marshal(t.EvidenceIDs)
	if evid == nil {
		evid = []byte("[]")
	}
	_, err := r.db.Exec(ctx, `
		INSERT INTO outreach_touchpoints (
			id, organization_id, account_id, contact_candidate_id,
			ordinal, cadence_step, channel, purpose, due_at, state, draft_id,
			recipient, subject, body_text,
			content_hash, approved_content_hash, approved_by, approved_at,
			authorization_mode,
			campaign_policy_authorization_id, authorization_policy_hash, authorization_at, signature_version,
			queued_at, sent_at, provider_message_id, stop_reason,
			previous_touchpoint_id, idempotency_key,
			policy_version, service_code, fact_used, evidence_ids,
			generated_context_hash, source_run_id,
			created_at, updated_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37
		)`,
		t.ID, t.OrganizationID, t.AccountID, t.ContactCandidateID,
		t.Ordinal, t.CadenceStep, t.Channel, t.Purpose, t.DueAt, t.State, t.DraftID,
		t.Recipient, t.Subject, t.BodyText,
		t.ContentHash, t.ApprovedContentHash, t.ApprovedBy, t.ApprovedAt,
		t.AuthorizationMode,
		t.CampaignPolicyAuthorizationID, t.AuthorizationPolicyHash, t.AuthorizationAt, t.SignatureVersion,
		t.QueuedAt, t.SentAt, t.ProviderMessageID, t.StopReason,
		t.PreviousTouchpointID, t.IdempotencyKey,
		t.PolicyVersion, t.ServiceCode, t.FactUsed, evid,
		t.GeneratedContextHash, t.SourceRunID,
		t.CreatedAt, t.UpdatedAt,
	)
	return err
}

func (r *outreachRepository) UpdateTouchpoint(ctx context.Context, t *models.OutreachTouchpoint) error {
	t.UpdatedAt = time.Now().UTC()
	evid, _ := json.Marshal(t.EvidenceIDs)
	if evid == nil {
		evid = []byte("[]")
	}
	ct, err := r.db.Exec(ctx, `
		UPDATE outreach_touchpoints SET
			contact_candidate_id=$3, channel=$4, purpose=$5, due_at=$6, state=$7, draft_id=$8,
			recipient=$9, subject=$10, body_text=$11,
			content_hash=$12, approved_content_hash=$13, approved_by=$14, approved_at=$15,
			authorization_mode=$16,
			campaign_policy_authorization_id=$17, authorization_policy_hash=$18, authorization_at=$19, signature_version=$20,
			queued_at=$21, sent_at=$22, provider_message_id=$23, stop_reason=$24,
			service_code=$25, fact_used=$26, evidence_ids=$27, generated_context_hash=$28,
			source_run_id=$29, updated_at=$30
		WHERE organization_id=$1 AND id=$2`,
		t.OrganizationID, t.ID,
		t.ContactCandidateID, t.Channel, t.Purpose, t.DueAt, t.State, t.DraftID,
		t.Recipient, t.Subject, t.BodyText,
		t.ContentHash, t.ApprovedContentHash, t.ApprovedBy, t.ApprovedAt,
		t.AuthorizationMode,
		t.CampaignPolicyAuthorizationID, t.AuthorizationPolicyHash, t.AuthorizationAt, t.SignatureVersion,
		t.QueuedAt, t.SentAt, t.ProviderMessageID, t.StopReason,
		t.ServiceCode, t.FactUsed, evid, t.GeneratedContextHash, t.SourceRunID, t.UpdatedAt,
	)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return errors.New("touchpoint not found")
	}
	return nil
}

func (r *outreachRepository) GetTouchpoint(ctx context.Context, orgID, id uuid.UUID) (*models.OutreachTouchpoint, error) {
	row := r.db.QueryRow(ctx, outreachTouchpointSelect+` FROM outreach_touchpoints t WHERE organization_id=$1 AND id=$2`, orgID, id)
	t, err := scanTouchpoint(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

func (r *outreachRepository) GetTouchpointByIdempotency(ctx context.Context, orgID uuid.UUID, key string) (*models.OutreachTouchpoint, error) {
	if key == "" {
		return nil, nil
	}
	row := r.db.QueryRow(ctx, outreachTouchpointSelect+` FROM outreach_touchpoints t WHERE organization_id=$1 AND idempotency_key=$2`, orgID, key)
	t, err := scanTouchpoint(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

func (r *outreachRepository) GetTouchpointByDraft(ctx context.Context, orgID, draftID uuid.UUID) (*models.OutreachTouchpoint, error) {
	row := r.db.QueryRow(ctx, outreachTouchpointSelect+` FROM outreach_touchpoints t WHERE organization_id=$1 AND draft_id=$2 ORDER BY updated_at DESC LIMIT 1`, orgID, draftID)
	t, err := scanTouchpoint(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

// GetTouchpointByProviderMessageID resolves a DSN/reply to the exact outbound
// touchpoint. Angle brackets are normalized because RFC Message-ID headers and
// provider callbacks do not consistently retain them.
func (r *outreachRepository) GetTouchpointByProviderMessageID(ctx context.Context, orgID uuid.UUID, messageID string) (*models.OutreachTouchpoint, error) {
	messageID = strings.Trim(strings.TrimSpace(messageID), "<>")
	if messageID == "" {
		return nil, nil
	}
	row := r.db.QueryRow(ctx, outreachTouchpointSelect+`
		FROM outreach_touchpoints t
		WHERE organization_id=$1
		  AND trim(both '<>' from provider_message_id)=$2
		ORDER BY updated_at DESC
		LIMIT 1`, orgID, messageID)
	t, err := scanTouchpoint(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

func (r *outreachRepository) ListTouchpoints(ctx context.Context, orgID, accountID uuid.UUID, state string, limit, offset int) ([]models.OutreachTouchpoint, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	var rows pgx.Rows
	var err error
	if state != "" {
		rows, err = r.db.Query(ctx, outreachTouchpointSelect+` FROM outreach_touchpoints t WHERE organization_id=$1 AND account_id=$2 AND state=$3 ORDER BY ordinal ASC LIMIT $4 OFFSET $5`, orgID, accountID, state, limit, offset)
	} else {
		rows, err = r.db.Query(ctx, outreachTouchpointSelect+` FROM outreach_touchpoints t WHERE organization_id=$1 AND account_id=$2 ORDER BY ordinal ASC LIMIT $3 OFFSET $4`, orgID, accountID, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectTouchpoints(rows)
}

func (r *outreachRepository) ListReviewTouchpoints(ctx context.Context, orgID uuid.UUID, limit, offset int) ([]models.OutreachTouchpoint, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	// Join account so the review queue can show company context without N+1.
	const q = `
		SELECT t.id, t.organization_id, t.account_id, t.contact_candidate_id,
			t.ordinal, COALESCE(t.cadence_step,''), COALESCE(t.channel,'EMAIL'), COALESCE(t.purpose,''),
			t.due_at, t.state, t.draft_id,
			COALESCE(t.recipient,''), COALESCE(t.subject,''), COALESCE(t.body_text,''),
			COALESCE(t.content_hash,''), COALESCE(t.approved_content_hash,''), t.approved_by, t.approved_at,
			t.queued_at, t.sent_at, COALESCE(t.provider_message_id,''), COALESCE(t.stop_reason,''),
			t.previous_touchpoint_id, COALESCE(t.idempotency_key,''),
			COALESCE(t.policy_version,''), COALESCE(t.service_code,''), COALESCE(t.fact_used,''), t.evidence_ids,
			t.created_at, t.updated_at,
			a.id, a.organization_id, COALESCE(a.cnpj14,''), COALESCE(a.razao_social,''), COALESCE(a.nome_fantasia,''),
			COALESCE(a.municipio,''), COALESCE(a.uf,''), COALESCE(a.service_code,''), COALESCE(a.queue_state,''),
			COALESCE(a.fact_to_mention,''), a.blocked, a.do_not_contact
		FROM outreach_touchpoints t
		LEFT JOIN outreach_accounts a ON a.id = t.account_id AND a.organization_id = t.organization_id
		WHERE t.organization_id=$1 AND t.state IN (
			'DUE','DRAFTED','AI_REWRITE_PENDING','ENRICHMENT_PENDING',
			'REJECTED_REWRITE_PENDING','NEEDS_REVIEW','APPROVED'
		)
		AND NOT (t.source_run_id<>'' AND t.idempotency_key LIKE 'prepared-initial:%')
		ORDER BY t.due_at ASC, t.ordinal ASC
		LIMIT $2 OFFSET $3`
	rows, err := r.db.Query(ctx, q, orgID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.OutreachTouchpoint
	for rows.Next() {
		var t models.OutreachTouchpoint
		var evid []byte
		var accID *uuid.UUID
		var accOrg *uuid.UUID
		var cnpj, razao, fantasia, mun, uf, svc, qstate, fact string
		var blocked, dnc bool
		err := rows.Scan(
			&t.ID, &t.OrganizationID, &t.AccountID, &t.ContactCandidateID,
			&t.Ordinal, &t.CadenceStep, &t.Channel, &t.Purpose,
			&t.DueAt, &t.State, &t.DraftID,
			&t.Recipient, &t.Subject, &t.BodyText,
			&t.ContentHash, &t.ApprovedContentHash, &t.ApprovedBy, &t.ApprovedAt,
			&t.QueuedAt, &t.SentAt, &t.ProviderMessageID, &t.StopReason,
			&t.PreviousTouchpointID, &t.IdempotencyKey,
			&t.PolicyVersion, &t.ServiceCode, &t.FactUsed, &evid,
			&t.CreatedAt, &t.UpdatedAt,
			&accID, &accOrg, &cnpj, &razao, &fantasia, &mun, &uf, &svc, &qstate, &fact, &blocked, &dnc,
		)
		if err != nil {
			return nil, err
		}
		_ = json.Unmarshal(evid, &t.EvidenceIDs)
		if accID != nil {
			t.Account = &models.OutreachAccount{
				ID: *accID, OrganizationID: orgID, CNPJ14: cnpj,
				RazaoSocial: razao, NomeFantasia: fantasia, Municipio: mun, UF: uf,
				ServiceCode: svc, QueueState: qstate, FactToMention: fact,
				Blocked: blocked, DoNotContact: dnc,
			}
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func collectTouchpoints(rows pgx.Rows) ([]models.OutreachTouchpoint, error) {
	var out []models.OutreachTouchpoint
	for rows.Next() {
		t, err := scanTouchpoint(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func (r *outreachRepository) CASQueueTouchpoint(ctx context.Context, orgID, id uuid.UUID, expectedContentHash string) (*models.OutreachTouchpoint, error) {
	now := time.Now().UTC()
	// Must match outreachTouchpointSelect / scanTouchpoint field count.
	const ret = `id, organization_id, account_id, contact_candidate_id, ordinal, COALESCE(cadence_step,''), COALESCE(channel,'EMAIL'), COALESCE(purpose,''), due_at, state, draft_id, COALESCE(recipient,''), COALESCE(subject,''), COALESCE(body_text,''), COALESCE(content_hash,''), COALESCE(approved_content_hash,''), approved_by, approved_at, COALESCE(authorization_mode,''), campaign_policy_authorization_id, COALESCE(authorization_policy_hash,''), authorization_at, COALESCE(signature_version,''), queued_at, sent_at, COALESCE(provider_message_id,''), COALESCE(stop_reason,''), previous_touchpoint_id, COALESCE(idempotency_key,''), COALESCE(policy_version,''), COALESCE(service_code,''), COALESCE(fact_used,''), evidence_ids, COALESCE(generated_context_hash,''), COALESCE(source_run_id,''), created_at, updated_at`
	// Human path: approved_by set. Policy path: authorization_mode=CAMPAIGN_POLICY and approved_by null.
	row := r.db.QueryRow(ctx, `
		UPDATE outreach_touchpoints
		SET state='QUEUED', queued_at=$4, updated_at=$4
		WHERE organization_id=$1 AND id=$2 AND state='APPROVED'
		  AND content_hash=$3 AND approved_content_hash=content_hash
		  AND (
		    approved_by IS NOT NULL
		    OR (authorization_mode = 'CAMPAIGN_POLICY' AND approved_by IS NULL)
		  )
		RETURNING `+ret, orgID, id, expectedContentHash, now)
	t, err := scanTouchpoint(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

func (r *outreachRepository) CASScheduleTouchpoint(ctx context.Context, orgID, id uuid.UUID, expectedContentHash, messageKey string, dueAt time.Time) (*models.OutreachTouchpoint, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if dueAt.IsZero() {
		dueAt = time.Now().UTC()
	}
	now := time.Now().UTC()
	const ret = `id, organization_id, account_id, contact_candidate_id, ordinal, COALESCE(cadence_step,''), COALESCE(channel,'EMAIL'), COALESCE(purpose,''), due_at, state, draft_id, COALESCE(recipient,''), COALESCE(subject,''), COALESCE(body_text,''), COALESCE(content_hash,''), COALESCE(approved_content_hash,''), approved_by, approved_at, COALESCE(authorization_mode,''), campaign_policy_authorization_id, COALESCE(authorization_policy_hash,''), authorization_at, COALESCE(signature_version,''), queued_at, sent_at, COALESCE(provider_message_id,''), COALESCE(stop_reason,''), previous_touchpoint_id, COALESCE(idempotency_key,''), COALESCE(policy_version,''), COALESCE(service_code,''), COALESCE(fact_used,''), evidence_ids, COALESCE(generated_context_hash,''), COALESCE(source_run_id,''), created_at, updated_at`
	row := tx.QueryRow(ctx, `
		UPDATE outreach_touchpoints
		SET state='QUEUED', queued_at=$4, due_at=$5, updated_at=$4
		WHERE organization_id=$1 AND id=$2 AND state='APPROVED'
		  AND content_hash=$3 AND approved_content_hash=content_hash
		  AND (approved_by IS NOT NULL OR authorization_mode='CAMPAIGN_POLICY')
		RETURNING `+ret, orgID, id, expectedContentHash, now, dueAt.UTC())
	tp, err := scanTouchpoint(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if tp.DraftID == nil {
		return nil, errors.New("approved touchpoint has no draft")
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO confenge_dispatch_queue (
			organization_id, channel, draft_id, message_key, recipient_ref,
			due_at, priority, status, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,0,'queued',$7,$7)
		ON CONFLICT (message_key) DO UPDATE SET
			due_at=EXCLUDED.due_at, recipient_ref=EXCLUDED.recipient_ref,
			status=CASE
				WHEN confenge_dispatch_queue.status='cancelled'
				  AND confenge_dispatch_queue.cancel_reason IN (
					'delegated_authority_or_source_binding_advanced',
					'source_run_superseded'
				  ) THEN 'queued'
				WHEN confenge_dispatch_queue.status IN ('sent','cancelled') THEN confenge_dispatch_queue.status
				ELSE 'queued'
			END,
			cancel_reason=CASE
				WHEN confenge_dispatch_queue.status='cancelled'
				  AND confenge_dispatch_queue.cancel_reason NOT IN (
					'delegated_authority_or_source_binding_advanced',
					'source_run_superseded'
				  )
				  THEN confenge_dispatch_queue.cancel_reason ELSE '' END,
			last_error=CASE
				WHEN confenge_dispatch_queue.status='cancelled'
				  AND confenge_dispatch_queue.cancel_reason NOT IN (
					'delegated_authority_or_source_binding_advanced',
					'source_run_superseded'
				  )
				  THEN confenge_dispatch_queue.last_error ELSE '' END,
			reserved_until=NULL, updated_at=EXCLUDED.updated_at`,
		orgID, tp.Channel, *tp.DraftID, messageKey, strings.ToLower(strings.TrimSpace(tp.Recipient)), dueAt.UTC(), now)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return tp, nil
}

func (r *outreachRepository) CancelOpenTouchpoints(ctx context.Context, orgID, accountID uuid.UUID, terminalState, stopReason string) (int, error) {
	if terminalState == "" {
		terminalState = models.TouchpointCancelled
	}
	now := time.Now().UTC()
	ct, err := r.db.Exec(ctx, `UPDATE outreach_touchpoints SET state=$4, stop_reason=$5, approved_by=NULL, approved_at=NULL, approved_content_hash='', authorization_mode='', campaign_policy_authorization_id=NULL, authorization_policy_hash='', authorization_at=NULL, updated_at=$6 WHERE organization_id=$1 AND account_id=$2 AND state=ANY($3::text[])`,
		orgID, accountID, []string{
			models.TouchpointPlanned, models.TouchpointDue, models.TouchpointDrafted,
			models.TouchpointAIRewritePending, models.TouchpointEnrichmentPending,
			models.TouchpointRejectedRewritePending, models.TouchpointNeedsReview,
			models.TouchpointApproved, models.TouchpointQueued,
		}, terminalState, stopReason, now)
	if err != nil {
		return 0, err
	}
	return int(ct.RowsAffected()), nil
}

// GetOutreachRecipientSuppression reads the platform's canonical suppression row.
func (r *outreachRepository) GetOutreachRecipientSuppression(ctx context.Context, orgID uuid.UUID, email string) (*models.SuppressedRecipient, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil, nil
	}
	var out models.SuppressedRecipient
	var metadata []byte
	err := r.db.QueryRow(ctx, `
		SELECT id, organization_id, email, reason, source, campaign_id,
			expires_at, metadata, created_at, updated_at
		FROM suppressed_recipients
		WHERE organization_id=$1 AND lower(email)=$2
		  AND (expires_at IS NULL OR expires_at > now())`, orgID, email).Scan(
		&out.ID, &out.OrganizationID, &out.Email, &out.Reason, &out.Source,
		&out.CampaignID, &out.ExpiresAt, &metadata, &out.CreatedAt, &out.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(metadata, &out.Metadata)
	return &out, nil
}

// UpsertOutreachRecipientSuppression writes the canonical recipient row idempotently.
func (r *outreachRepository) UpsertOutreachRecipientSuppression(ctx context.Context, suppression *models.SuppressedRecipient) error {
	if suppression == nil {
		return errors.New("recipient suppression is required")
	}
	email := strings.ToLower(strings.TrimSpace(suppression.Email))
	if suppression.OrganizationID == uuid.Nil || email == "" {
		return errors.New("organization and recipient are required")
	}
	metadata, err := json.Marshal(suppression.Metadata)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(ctx, `
		INSERT INTO suppressed_recipients (
			organization_id, email, reason, source, campaign_id, expires_at,
			metadata, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,now(),now())
		ON CONFLICT (organization_id,email) DO UPDATE SET
			reason=excluded.reason, source=excluded.source,
			campaign_id=COALESCE(excluded.campaign_id,suppressed_recipients.campaign_id),
			expires_at=excluded.expires_at, metadata=excluded.metadata, updated_at=now()`,
		suppression.OrganizationID, email, suppression.Reason, suppression.Source,
		suppression.CampaignID, suppression.ExpiresAt, metadata)
	return err
}

// CancelSuppressedOutreachRecipient projects suppression into exact-recipient work.
func (r *outreachRepository) CancelSuppressedOutreachRecipient(ctx context.Context, orgID uuid.UUID, email, terminalState, reason string) (int, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return 0, nil
	}
	if terminalState == "" {
		terminalState = models.TouchpointCancelled
	}
	if reason == "" {
		reason = "recipient_suppressed"
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	verification := ""
	setBounced, setDNC, setBlocked := false, false, false
	switch terminalState {
	case models.TouchpointBounced:
		verification, setBounced = models.OutreachVerifyBounced, true
	case models.TouchpointDNC:
		verification, setDNC = models.OutreachVerifyDoNotContact, true
	default:
		setBlocked = true
	}
	if _, err := tx.Exec(ctx, `
		UPDATE outreach_contact_candidates
		SET bounced=bounced OR $3, do_not_contact=do_not_contact OR $4,
			blocked=blocked OR $5, block_reason=CASE WHEN $3 OR $4 OR $5 THEN $6 ELSE block_reason END,
			verification_status=CASE WHEN $7<>'' THEN $7 ELSE verification_status END,
			updated_at=now()
		WHERE organization_id=$1 AND lower(email)=$2`, orgID, email,
		setBounced, setDNC, setBlocked, reason, verification); err != nil {
		return 0, err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE confenge_dispatch_queue q
		SET status='cancelled', cancel_reason=$3, last_error=$3,
			reserved_until=NULL, updated_at=now()
		FROM outreach_touchpoints t
		WHERE t.organization_id=$1 AND lower(t.recipient)=$2
		  AND t.draft_id=q.draft_id AND q.organization_id=t.organization_id
		  AND q.status IN ('queued','reserved')`, orgID, email, reason); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE confenge_delegated_first_touch_decisions d
		SET state='CANCELLED',
			blocker_codes=CASE WHEN d.blocker_codes @> to_jsonb(ARRAY[$3]::text[])
				THEN d.blocker_codes ELSE d.blocker_codes || to_jsonb(ARRAY[$3]::text[]) END,
			updated_at=now()
		FROM outreach_touchpoints t
		WHERE t.organization_id=$1 AND lower(t.recipient)=$2
		  AND d.organization_id=t.organization_id AND d.touchpoint_id=t.id
		  AND d.state IN ('APPROVED','QUEUED','APPROVED_NOT_SCHEDULED')`, orgID, email, reason); err != nil {
		return 0, err
	}
	ct, err := tx.Exec(ctx, `
		UPDATE outreach_touchpoints
		SET state=$3, stop_reason=$4, approved_by=NULL, approved_at=NULL,
			approved_content_hash='', authorization_mode='',
			campaign_policy_authorization_id=NULL, authorization_policy_hash='',
			authorization_at=NULL, updated_at=now()
		WHERE organization_id=$1 AND lower(recipient)=$2
		  AND state IN ('PLANNED','DUE','DRAFTED','AI_REWRITE_PENDING',
			'ENRICHMENT_PENDING','REJECTED_REWRITE_PENDING','NEEDS_REVIEW',
			'APPROVED','QUEUED')`, orgID, email, terminalState, reason)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return int(ct.RowsAffected()), nil
}

func (r *outreachRepository) ListDuePlannedTouchpoints(ctx context.Context, orgID uuid.UUID, now time.Time, limit int) ([]models.OutreachTouchpoint, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := r.db.Query(ctx, outreachTouchpointSelect+`
		FROM outreach_touchpoints t
		WHERE organization_id=$1
		  AND state = 'PLANNED'
		  AND due_at <= $2
		ORDER BY due_at ASC, ordinal ASC
		LIMIT $3`, orgID, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectTouchpoints(rows)
}
