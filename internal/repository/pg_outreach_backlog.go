package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const sourceRunSupersededStopReason = "source_run_superseded"

// Retirement is scoped to accounts that actually lost COMMERCIAL_AUTHORITY/2.0
// qualification. Producer cadence is acquisition provenance and never dequeues
// proven work, including members a newer run simply did not re-emit.
const supersededLostQualification = `AND NOT confenge_commercially_qualified(
	a.commercial_qualification_state,a.commercial_qualified_until,
	a.commercial_qualification_deactivated,CURRENT_DATE)`

func (r *outreachRepository) materializeCurrentInitialBacklog(ctx context.Context, orgID uuid.UUID, sourceRunID string) (OutreachInitialBacklogCounts, error) {
	var out OutreachInitialBacklogCounts
	sourceRunID = strings.TrimSpace(sourceRunID)
	if orgID == uuid.Nil || sourceRunID == "" {
		return out, fmt.Errorf("organization_id and source_run_id are required")
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err = tx.Exec(ctx, `
		UPDATE confenge_dispatch_queue q
		SET status='cancelled',cancel_reason=$3,last_error=$3,reserved_until=NULL,updated_at=now()
		FROM outreach_touchpoints t
		JOIN outreach_accounts a ON a.organization_id=t.organization_id AND a.id=t.account_id
		WHERE t.organization_id=$1 AND t.ordinal=1 AND t.purpose='INITIAL' AND t.channel='EMAIL'
		  AND COALESCE(t.source_run_id,'')<>$2 `+supersededLostQualification+` AND t.draft_id=q.draft_id
		  AND q.organization_id=t.organization_id AND q.status IN ('queued','reserved')`, orgID, sourceRunID, sourceRunSupersededStopReason); err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE confenge_delegated_first_touch_decisions d
		SET state='CANCELLED',blocker_codes=CASE
			WHEN blocker_codes @> '["source_run_superseded"]'::jsonb THEN blocker_codes
			ELSE blocker_codes || '["source_run_superseded"]'::jsonb END,updated_at=now()
		FROM outreach_touchpoints t
		JOIN outreach_accounts a ON a.organization_id=t.organization_id AND a.id=t.account_id
		WHERE t.organization_id=$1 AND t.ordinal=1 AND t.purpose='INITIAL' AND t.channel='EMAIL'
		  AND COALESCE(t.source_run_id,'')<>$2 `+supersededLostQualification+` AND d.organization_id=t.organization_id
		  AND d.touchpoint_id=t.id AND d.state IN ('APPROVED','QUEUED','APPROVED_NOT_SCHEDULED')`, orgID, sourceRunID); err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE outreach_drafts d
		SET status='BLOCKED',approved_by=NULL,approved_at=NULL,campaign_id=NULL,
			enrollment_contact_id=NULL,enrolled_at=NULL,updated_at=now()
		FROM outreach_touchpoints t
		JOIN outreach_accounts a ON a.organization_id=t.organization_id AND a.id=t.account_id
		WHERE t.organization_id=$1 AND t.ordinal=1 AND t.purpose='INITIAL' AND t.channel='EMAIL'
		  AND COALESCE(t.source_run_id,'')<>$2 `+supersededLostQualification+` AND d.organization_id=t.organization_id
		  AND d.id=t.draft_id AND d.status NOT IN ('SENT','BLOCKED')`, orgID, sourceRunID); err != nil {
		return out, err
	}
	retired, err := tx.Exec(ctx, `
		UPDATE outreach_touchpoints t
		SET state='CANCELLED',stop_reason=$3,approved_content_hash='',approved_by=NULL,
			approved_at=NULL,authorization_mode='',campaign_policy_authorization_id=NULL,
			authorization_policy_hash='',authorization_at=NULL,signature_version='',queued_at=NULL,
			delegated_reserved_until=NULL,delegated_last_error=$3,updated_at=now()
		FROM outreach_accounts a
		WHERE t.organization_id=$1 AND t.ordinal=1 AND t.purpose='INITIAL' AND t.channel='EMAIL'
		  AND a.organization_id=t.organization_id AND a.id=t.account_id
		  AND COALESCE(t.source_run_id,'')<>$2 `+supersededLostQualification+`
		  AND t.state IN ('PLANNED','DUE','DRAFTED','AI_REWRITE_PENDING','ENRICHMENT_PENDING',
			'REJECTED_REWRITE_PENDING','NEEDS_REVIEW','APPROVED','QUEUED')`, orgID, sourceRunID, sourceRunSupersededStopReason)
	if err != nil {
		return out, err
	}
	out.StaleRetired = int(retired.RowsAffected())

	if _, err = tx.Exec(ctx, `
		CREATE TEMP TABLE confenge_current_initial_backlog ON COMMIT DROP AS
		WITH terminal_accounts AS MATERIALIZED (
			SELECT id AS account_id
			FROM outreach_accounts
			WHERE organization_id=$1
			  AND queue_state IN ('SENT','REPLIED','MEETING','PROPOSAL','WON','LOST','ENROLLED')
			UNION
			SELECT account_id
			FROM confenge_delegated_first_touch_decisions
			WHERE organization_id=$1 AND state='SENT' AND account_id IS NOT NULL
			UNION
			SELECT t.account_id
			FROM confenge_dispatch_sends sent
			JOIN outreach_touchpoints t
			  ON t.organization_id=sent.organization_id AND (
			    t.draft_id=sent.draft_id OR EXISTS (
			      SELECT 1 FROM confenge_dispatch_queue q
			      WHERE q.organization_id=sent.organization_id AND q.id=sent.queue_id AND q.draft_id=t.draft_id
			    ))
			WHERE sent.organization_id=$1 AND sent.channel='EMAIL'
			UNION
			SELECT account_id
			FROM outreach_touchpoints
			WHERE organization_id=$1 AND ordinal=1 AND purpose='INITIAL' AND channel='EMAIL'
			  AND (state IN ('SENT','REPLIED') OR sent_at IS NOT NULL OR provider_message_id<>'')
		), candidate_rollup AS (
			SELECT a.id AS account_id,
				count(*) FILTER (WHERE c.discovery_json->>'preferred_initial'='true')::int AS preferred_count,
				(array_agg(c.id ORDER BY c.id) FILTER (WHERE c.discovery_json->>'preferred_initial'='true'))[1] AS candidate_id,
				bool_or(
					c.discovery_json->>'preferred_initial'='true'
					AND c.email<>'' AND NOT c.blocked AND NOT c.do_not_contact AND NOT c.bounced
					AND c.block_reason NOT ILIKE '%provenance_taint%'
					AND c.block_reason NOT ILIKE '%provenance_chain%'
					AND lower(c.email) NOT LIKE 'fixture@%' AND lower(c.email) NOT LIKE 'synthetic@%'
					AND (
						c.verification_status NOT IN ('CANDIDATE_UNVERIFIED','NOT_FOUND','INVALID','BOUNCED','DO_NOT_CONTACT')
						OR (
							c.discovery_json @> '{"controlled_email_eligible":true}'::jsonb
							AND upper(COALESCE(c.discovery_json->>'route_class','')) IN
								('DIRECT_PERSON','ROLE_OR_DEPARTMENT','GENERIC_COMPANY','PUBLIC_COMPANY_FREEMAIL')
						)
					)
				) FILTER (WHERE c.discovery_json->>'preferred_initial'='true') AS preferred_eligible,
				bool_or(
					c.discovery_json->>'preferred_initial'='true'
					AND c.email<>'' AND NOT c.blocked AND NOT c.do_not_contact AND NOT c.bounced
					AND c.block_reason NOT ILIKE '%provenance_taint%'
					AND c.block_reason NOT ILIKE '%provenance_chain%'
					AND lower(c.email) NOT LIKE 'fixture@%' AND lower(c.email) NOT LIKE 'synthetic@%'
					AND c.discovery_json @> '{"controlled_email_eligible":true}'::jsonb
					AND upper(COALESCE(c.discovery_json->>'route_class','')) IN
						('DIRECT_PERSON','ROLE_OR_DEPARTMENT','GENERIC_COMPANY','PUBLIC_COMPANY_FREEMAIL')
					AND upper(c.channel_epistemic_class)='OBSERVED'
					AND upper(c.route_freshness)='FRESH'
					AND upper(c.ownership_status)='COMPANY_OWNED'
					AND upper(c.route_suppression) IN ('','NONE')
					AND upper(c.email_derivation)<>'INFERRED'
					AND c.email LIKE '%@%' AND c.email !~ '[[:space:]]'
					AND (
						c.source_url ~* '^https?://'
						OR (
							lower(COALESCE(NULLIF(c.discovery_json->>'source',''), NULLIF(c.discovery_json->>'source_type',''), ''))
								IN ('company_registry', 'official_registry')
							AND lower(COALESCE(NULLIF(c.discovery_json->>'source_type',''), NULLIF(c.discovery_json->>'source',''), 'company_registry'))
								IN ('company_registry', 'real_registry', 'official_registry')
						)
						OR (
							upper(c.verification_status)='OFFICIAL_SOURCE'
							AND COALESCE(c.source_url,'')=''
						)
					)
					AND c.source_date BETWEEN CURRENT_DATE - 29 AND CURRENT_DATE
				) FILTER (WHERE c.discovery_json->>'preferred_initial'='true') AS preferred_delegated_eligible
			FROM outreach_accounts a
			LEFT JOIN outreach_contact_candidates c
			  ON c.organization_id=a.organization_id AND c.account_id=a.id
			 AND a.last_import_run_id IS NOT NULL AND c.last_import_run_id=a.last_import_run_id
			WHERE a.organization_id=$1
			  AND (a.source_run_id=$2 OR confenge_commercially_qualified(
				a.commercial_qualification_state,a.commercial_qualified_until,
				a.commercial_qualification_deactivated,CURRENT_DATE))
			GROUP BY a.id
		), classified AS (
			SELECT a.id AS account_id,cr.candidate_id,(a.source_run_id=$2) AS manifest_member,
				(a.contractor_role_status='CONTRACTOR_ROLE_CONFIRMED'
				 AND a.target_party_role='SUPPLIER'
				 AND a.supplier_cnpj14=a.cnpj14 AND a.contractor_role_evidence_hash ~ '^[0-9a-f]{64}$'
				 AND jsonb_array_length(a.contractor_role_evidence_ids)>0) AS supplier_confirmed,
				(COALESCE(cr.preferred_count,0)=1 AND cr.candidate_id IS NOT NULL) AS candidate_attributed,
				(terminal.account_id IS NULL AND a.target_fit_eligible AND (a.target_fit_fresh OR confenge_commercially_qualified(
					a.commercial_qualification_state,a.commercial_qualified_until,
					a.commercial_qualification_deactivated,CURRENT_DATE)) AND a.target_fit_class='TARGET_CONFIRMED'
				 AND a.target_fit_version<>'' AND a.target_fit_source_watermark<>''
				 AND a.target_fit_observed_at IS NOT NULL AND NOT a.blocked AND NOT a.do_not_contact
				 AND COALESCE(cr.preferred_count,0)=1 AND COALESCE(cr.preferred_eligible,false)) AS initial_prepared,
				(COALESCE(cr.preferred_count,0)=1 AND COALESCE(cr.preferred_delegated_eligible,false)) AS delegated_candidate,
				CASE
					WHEN terminal.account_id IS NOT NULL THEN 'initial_already_contacted'
					WHEN a.blocked OR a.do_not_contact THEN 'account_suppressed'
					WHEN NOT a.target_fit_eligible THEN COALESCE(NULLIF(a.target_fit_suppression_reason,''),'target_fit_ineligible')
					WHEN (NOT a.target_fit_fresh AND NOT confenge_commercially_qualified(
						a.commercial_qualification_state,a.commercial_qualified_until,
						a.commercial_qualification_deactivated,CURRENT_DATE)) OR a.target_fit_class<>'TARGET_CONFIRMED' THEN 'target_fit_not_current_confirmed'
					WHEN a.target_fit_version='' OR a.target_fit_source_watermark='' OR a.target_fit_observed_at IS NULL THEN 'target_fit_provenance_missing'
					WHEN COALESCE(cr.preferred_count,0)=0 THEN 'preferred_current_recipient_missing'
					WHEN cr.preferred_count>1 THEN 'preferred_current_recipient_conflict'
					WHEN NOT COALESCE(cr.preferred_eligible,false) THEN 'preferred_current_recipient_ineligible'
					WHEN a.contractor_role_status='PARTY_ROLE_CONFLICT' THEN 'supplier_role_conflict'
					WHEN a.contractor_role_status<>'CONTRACTOR_ROLE_CONFIRMED' OR a.target_party_role<>'SUPPLIER' THEN 'supplier_role_unknown'
					WHEN NOT confenge_commercially_qualified(
						a.commercial_qualification_state,a.commercial_qualified_until,
						a.commercial_qualification_deactivated,CURRENT_DATE) THEN 'commercial_qualification_not_current'
					WHEN a.supplier_cnpj14<>a.cnpj14 THEN 'supplier_identity_mismatch'
					WHEN a.contractor_role_evidence_hash !~ '^[0-9a-f]{64}$' OR jsonb_array_length(a.contractor_role_evidence_ids)=0 THEN 'supplier_role_evidence_incomplete'
					WHEN NOT COALESCE(cr.preferred_delegated_eligible,false) THEN 'preferred_current_recipient_not_delegated'
					ELSE ''
				END AS reason_code
			FROM outreach_accounts a
			LEFT JOIN candidate_rollup cr ON cr.account_id=a.id
			LEFT JOIN terminal_accounts terminal ON terminal.account_id=a.id
			WHERE a.organization_id=$1
			  AND (a.source_run_id=$2 OR confenge_commercially_qualified(
				a.commercial_qualification_state,a.commercial_qualified_until,
				a.commercial_qualification_deactivated,CURRENT_DATE))
		)
		SELECT account_id,candidate_id,manifest_member,supplier_confirmed,candidate_attributed,initial_prepared,
			(initial_prepared AND supplier_confirmed AND delegated_candidate AND reason_code='') AS delegated_eligible,reason_code
		FROM classified`, orgID, sourceRunID); err != nil {
		return out, err
	}

	if _, err = tx.Exec(ctx, `
		UPDATE outreach_accounts a
		SET initial_backlog_reason_code=b.reason_code,updated_at=now()
		FROM confenge_current_initial_backlog b
		WHERE a.organization_id=$1 AND a.id=b.account_id`, orgID); err != nil {
		return out, err
	}

	const terminalDuplicateReason = "initial_already_contacted"
	if _, err = tx.Exec(ctx, `
		UPDATE confenge_dispatch_reservations r
		SET state='released',last_error=$2
		FROM confenge_dispatch_queue q
		JOIN outreach_touchpoints t
		  ON t.organization_id=q.organization_id AND t.draft_id=q.draft_id
		JOIN confenge_current_initial_backlog b ON b.account_id=t.account_id
		WHERE q.organization_id=$1 AND b.reason_code=$2
		  AND t.ordinal=1 AND t.purpose='INITIAL' AND t.channel='EMAIL'
		  AND t.state IN ('PLANNED','DUE','DRAFTED','AI_REWRITE_PENDING','ENRICHMENT_PENDING',
		    'REJECTED_REWRITE_PENDING','NEEDS_REVIEW','APPROVED','QUEUED')
		  AND r.organization_id=q.organization_id AND r.message_key=q.message_key AND r.state='reserved'`,
		orgID, terminalDuplicateReason); err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE confenge_dispatch_queue q
		SET status='cancelled',cancel_reason=$2,last_error=$2,reserved_until=NULL,updated_at=now()
		FROM outreach_touchpoints t
		JOIN confenge_current_initial_backlog b ON b.account_id=t.account_id
		WHERE q.organization_id=$1 AND b.reason_code=$2
		  AND t.organization_id=q.organization_id AND t.draft_id=q.draft_id
		  AND t.ordinal=1 AND t.purpose='INITIAL' AND t.channel='EMAIL'
		  AND t.state IN ('PLANNED','DUE','DRAFTED','AI_REWRITE_PENDING','ENRICHMENT_PENDING',
		    'REJECTED_REWRITE_PENDING','NEEDS_REVIEW','APPROVED','QUEUED')
		  AND q.status IN ('queued','reserved')`, orgID, terminalDuplicateReason); err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE confenge_delegated_first_touch_decisions d
		SET state='CANCELLED',blocker_codes=CASE WHEN blocker_codes ? $2 THEN blocker_codes
		  ELSE blocker_codes || jsonb_build_array($2::text) END,updated_at=now()
		FROM confenge_current_initial_backlog b
		WHERE d.organization_id=$1 AND b.account_id=d.account_id AND b.reason_code=$2
		  AND d.state IN ('APPROVED','QUEUED','APPROVED_NOT_SCHEDULED')`, orgID, terminalDuplicateReason); err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE outreach_drafts d
		SET status='BLOCKED',approved_by=NULL,approved_at=NULL,campaign_id=NULL,
		  enrollment_contact_id=NULL,enrolled_at=NULL,updated_at=now()
		FROM outreach_touchpoints t
		JOIN confenge_current_initial_backlog b ON b.account_id=t.account_id
		WHERE d.organization_id=$1 AND b.reason_code=$2
		  AND t.organization_id=d.organization_id AND t.draft_id=d.id
		  AND t.ordinal=1 AND t.purpose='INITIAL' AND t.channel='EMAIL'
		  AND t.state IN ('PLANNED','DUE','DRAFTED','AI_REWRITE_PENDING','ENRICHMENT_PENDING',
		    'REJECTED_REWRITE_PENDING','NEEDS_REVIEW','APPROVED','QUEUED')
		  AND d.status<>'SENT'`, orgID, terminalDuplicateReason); err != nil {
		return out, err
	}
	terminalRetired, terminalErr := tx.Exec(ctx, `
		UPDATE outreach_touchpoints t
		SET state='CANCELLED',stop_reason=$2,approved_content_hash='',approved_by=NULL,
		  approved_at=NULL,authorization_mode='',campaign_policy_authorization_id=NULL,
		  authorization_policy_hash='',authorization_at=NULL,signature_version='',queued_at=NULL,
		  delegated_reserved_until=NULL,delegated_last_error=$2,updated_at=now()
		FROM confenge_current_initial_backlog b
		WHERE t.organization_id=$1 AND b.account_id=t.account_id AND b.reason_code=$2
		  AND t.ordinal=1 AND t.purpose='INITIAL' AND t.channel='EMAIL'
		  AND t.state IN ('PLANNED','DUE','DRAFTED','AI_REWRITE_PENDING','ENRICHMENT_PENDING',
		    'REJECTED_REWRITE_PENDING','NEEDS_REVIEW','APPROVED','QUEUED')`, orgID, terminalDuplicateReason)
	if terminalErr != nil {
		return out, terminalErr
	}
	out.TerminalRetired = int(terminalRetired.RowsAffected())
	if _, err = tx.Exec(ctx, `
		INSERT INTO outreach_touchpoints (
			organization_id,account_id,contact_candidate_id,ordinal,cadence_step,channel,purpose,
			due_at,state,recipient,idempotency_key,policy_version,service_code,fact_used,evidence_ids,
			generated_context_hash,source_run_id,delegated_retry_at
		)
		SELECT a.organization_id,a.id,b.candidate_id,1,'INITIAL','EMAIL','INITIAL',now(),'DUE',lower(c.email),
			'prepared-initial:' || $2 || ':' || a.id::text,'confenge.cadence.v1',a.service_code,
				a.fact_to_mention,CASE WHEN jsonb_array_length(a.contractor_role_evidence_ids)>0
					THEN a.contractor_role_evidence_ids ELSE a.moment_evidence_ids END,
			a.message_context_hash,$2,now()
		FROM confenge_current_initial_backlog b
		JOIN outreach_accounts a ON a.organization_id=$1 AND a.id=b.account_id
		JOIN outreach_contact_candidates c ON c.organization_id=a.organization_id AND c.id=b.candidate_id
		WHERE b.initial_prepared
		ON CONFLICT DO NOTHING`, orgID, sourceRunID); err != nil {
		return out, err
	}

	if err = tx.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE manifest_member)::int,
			count(*) FILTER (WHERE manifest_member AND supplier_confirmed)::int,
			count(*) FILTER (WHERE manifest_member AND candidate_attributed)::int,
			count(*) FILTER (WHERE manifest_member AND initial_prepared)::int,
			count(*) FILTER (WHERE manifest_member AND delegated_eligible)::int,
			count(*) FILTER (WHERE manifest_member AND NOT delegated_eligible)::int,
			count(*) FILTER (WHERE NOT manifest_member)::int,
			count(*) FILTER (WHERE NOT manifest_member AND supplier_confirmed)::int,
			count(*) FILTER (WHERE NOT manifest_member AND candidate_attributed)::int,
			count(*) FILTER (WHERE NOT manifest_member AND initial_prepared)::int,
			count(*) FILTER (WHERE NOT manifest_member AND delegated_eligible)::int,
			count(*) FILTER (WHERE NOT manifest_member AND NOT delegated_eligible)::int
		FROM confenge_current_initial_backlog`).Scan(
		&out.Imported, &out.SupplierConfirmed, &out.CandidateAttributed,
		&out.InitialPrepared, &out.DelegatedEligible, &out.HeldException,
		&out.CarryoverImported, &out.CarryoverSupplierConfirmed, &out.CarryoverCandidateAttributed,
		&out.CarryoverInitialPrepared, &out.CarryoverDelegatedEligible, &out.CarryoverHeldException,
	); err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, err
	}
	return out, nil
}
