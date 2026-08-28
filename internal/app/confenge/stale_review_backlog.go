package confenge

import (
	"context"

	"github.com/google/uuid"
)

const staleReviewStopReason = "recipient_not_in_current_snapshot"

// A recipient is retired only on actual invalidity. A candidate whose
// last_import_run_id merely lags the account's newest import was not re-emitted;
// that is acquisition provenance and never invalidates a proven first touch.
const staleReviewRecipientInvalid = `
			c.id IS NULL
			OR c.blocked OR c.bounced OR c.do_not_contact
			OR upper(COALESCE(c.verification_status,'')) IN ('INVALID','BOUNCED','DO_NOT_CONTACT')
			OR upper(COALESCE(c.route_suppression,'')) NOT IN ('','NONE')
			OR c.block_reason ILIKE '%superseded%'`

// retireStaleReviewBacklog removes review authority from first-touch drafts
// whose bound recipient is actually invalid. Accounts with another eligible
// route are returned to asynchronous generation; no approval, queue or
// transport authority is created here.
func (s *service) retireStaleReviewBacklog(ctx context.Context, orgID uuid.UUID) (retired, requeued int, err error) {
	if s == nil || s.humanGateDB == nil {
		return 0, 0, nil
	}
	tx, err := s.humanGateDB.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		SELECT t.id,COALESCE(t.draft_id,'00000000-0000-0000-0000-000000000000'::uuid),t.account_id
		FROM outreach_touchpoints t
		JOIN outreach_accounts a
		  ON a.organization_id=t.organization_id AND a.id=t.account_id
		LEFT JOIN outreach_contact_candidates c
		  ON c.organization_id=t.organization_id AND c.id=t.contact_candidate_id
		WHERE t.organization_id=$1
		  AND t.ordinal=1
		  AND t.state='NEEDS_REVIEW'
		  AND (`+staleReviewRecipientInvalid+`)
		FOR UPDATE OF t,a`, orgID)
	if err != nil {
		return 0, 0, err
	}
	var touchpointIDs, accountIDs []uuid.UUID
	accountSeen := map[uuid.UUID]bool{}
	for rows.Next() {
		var touchpointID, draftID, accountID uuid.UUID
		if err = rows.Scan(&touchpointID, &draftID, &accountID); err != nil {
			rows.Close()
			return 0, 0, err
		}
		touchpointIDs = append(touchpointIDs, touchpointID)
		if !accountSeen[accountID] {
			accountSeen[accountID] = true
			accountIDs = append(accountIDs, accountID)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, 0, err
	}
	rows.Close()
	if len(touchpointIDs) == 0 {
		return 0, 0, tx.Commit(ctx)
	}

	if _, err = tx.Exec(ctx, `
			UPDATE outreach_drafts
			SET status='BLOCKED',approved_by=NULL,approved_at=NULL,
				campaign_id=NULL,enrollment_contact_id=NULL,enrolled_at=NULL,updated_at=now()
			WHERE organization_id=$1 AND account_id=ANY($2::uuid[])
			  AND status IN ('NOT_GENERATED','GENERATING','AI_REWRITE_PENDING','ENRICHMENT_PENDING','REJECTED_REWRITE_PENDING','NEEDS_REVIEW')`, orgID, accountIDs); err != nil {
		return 0, 0, err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE outreach_touchpoints
		SET state='CANCELLED',stop_reason=$3,approved_content_hash='',
			approved_by=NULL,approved_at=NULL,authorization_mode='',
			campaign_policy_authorization_id=NULL,authorization_policy_hash='',
			authorization_at=NULL,signature_version='',queued_at=NULL,updated_at=now()
		WHERE organization_id=$1 AND account_id=ANY($2::uuid[])
		  AND state IN ('PLANNED','DUE','DRAFTED','AI_REWRITE_PENDING','ENRICHMENT_PENDING','REJECTED_REWRITE_PENDING','NEEDS_REVIEW')`,
		orgID, accountIDs, staleReviewStopReason); err != nil {
		return 0, 0, err
	}
	retired = len(touchpointIDs)

	// First make the account state truthful even when no replacement route is
	// available. The second update promotes only accounts with a current route.
	if _, err = tx.Exec(ctx, `
		UPDATE outreach_accounts a
		SET queue_state='NEEDS_CONTACT',draft_generation_reserved_until=NULL,
			draft_generation_last_error=$3,updated_at=now()
		WHERE a.organization_id=$1 AND a.id=ANY($2::uuid[])
		  AND a.queue_state='NEEDS_REVIEW'
		  AND NOT EXISTS (
			SELECT 1 FROM outreach_touchpoints active
			WHERE active.organization_id=a.organization_id AND active.account_id=a.id
			  AND active.state IN ('AI_REWRITE_PENDING','ENRICHMENT_PENDING','REJECTED_REWRITE_PENDING','NEEDS_REVIEW','APPROVED','QUEUED')
		  )`, orgID, accountIDs, staleReviewStopReason); err != nil {
		return 0, 0, err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE outreach_accounts a
		SET queue_state='READY_TO_GENERATE',draft_generation_retry_at=now(),
			draft_generation_reserved_until=NULL,draft_generation_last_error='',updated_at=now()
		WHERE a.organization_id=$1 AND a.id=ANY($2::uuid[])
		  AND a.queue_state='NEEDS_CONTACT'
		  AND a.target_fit_eligible=true AND a.target_fit_fresh=true
		  AND a.target_fit_class='TARGET_CONFIRMED'
		  AND a.blocked=false AND a.do_not_contact=false
		  AND EXISTS (
			SELECT 1 FROM outreach_contact_candidates c
			WHERE c.organization_id=a.organization_id AND c.account_id=a.id
			  AND c.email<>'' AND c.blocked=false AND c.do_not_contact=false AND c.bounced=false
			  AND upper(COALESCE(c.route_suppression,'')) IN ('','NONE')
			  AND (
				(c.email_send_ready=true AND c.mailbox_purpose_send_blocked=false
				 AND c.verification_status NOT IN ('CANDIDATE_UNVERIFIED','NOT_FOUND','INVALID','BOUNCED','DO_NOT_CONTACT'))
				OR (c.discovery_json @> '{"controlled_email_eligible":true}'::jsonb
				 AND upper(COALESCE(c.discovery_json->>'route_class','')) IN
				 ('DIRECT_PERSON','ROLE_OR_DEPARTMENT','GENERIC_COMPANY','PUBLIC_COMPANY_FREEMAIL'))
			  )
		  )`, orgID, accountIDs)
	if err != nil {
		return 0, 0, err
	}
	requeued = int(tag.RowsAffected())
	if err = tx.Commit(ctx); err != nil {
		return 0, 0, err
	}
	return retired, requeued, nil
}
