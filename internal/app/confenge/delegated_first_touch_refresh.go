package confenge

import (
	"context"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

const delegatedBindingRefreshReason = "delegated_authority_or_source_binding_advanced"

// retireStaleDelegatedFirstTouches revokes approvals whose account is no longer
// commercially QUALIFIED, whose policy binding moved, or whose campaign policy
// authorization is gone. Acquisition and build provenance never retire a
// still-qualified decision: source run id, snapshot expiry, freshness hash, the
// population attestation revision and the runtime release sha are all import or
// build identity rather than authority, so sourceRunID and snapshotHash are
// accepted for call-site symmetry and deliberately not compared.
//
// Retirement is destructive and irreversible for a queued touch, so it requires
// positive proof of revocation. Doubt (an unreadable feed, a structurally
// invalid attestation) blocks transport elsewhere and retires nothing here.
func (s *service) retireStaleDelegatedFirstTouches(ctx context.Context, orgID uuid.UUID, sourceRunID, snapshotHash string, policyAuthorizationIDs ...*uuid.UUID) (int, error) {
	_, _ = sourceRunID, snapshotHash
	if s == nil || s.delegatedDB == nil {
		return 0, nil
	}
	tx, err := s.delegatedDB.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var policyAuthorizationID *uuid.UUID
	checkPolicyAuthorization := len(policyAuthorizationIDs) > 0
	if len(policyAuthorizationIDs) > 0 {
		policyAuthorizationID = policyAuthorizationIDs[0]
	}
	feedState, feedErr := s.repo.GetFeedSyncState(ctx, orgID)
	// A feed that could not be read has revoked nothing. Sweeping on an unknown
	// population would cancel every approved touch on a transient database error.
	if feedErr != nil {
		return 0, feedErr
	}
	if feedState == nil || validateAuthoritativeFeedStructure(feedState, true) != nil {
		return 0, nil
	}
	// A producer that published no population attestation has not revoked
	// anything. Fall back to the durable per-account three-year evidence, the
	// same authority the first-window readback reads, so an unattested but
	// individually proven population never retires its own approved work.
	authority := FeedCommercialAuthorityState(feedState)
	if !authority.Present {
		authority = s.commercialQualificationFromAccounts(ctx, orgID, s.now())
	}
	// Symmetric with the approval path, which blocks only on a present-and-not-
	// qualified attestation. Absence of proof is not proof of revocation.
	authorityRevoked := authority.Present && authority.State != CommercialQualified

	rows, err := tx.Query(ctx, `
		SELECT d.touchpoint_id
		FROM confenge_delegated_first_touch_decisions d
		LEFT JOIN outreach_accounts a
		  ON a.organization_id=d.organization_id AND a.id=d.account_id
		WHERE d.organization_id=$1
		  AND d.state IN ('APPROVED','QUEUED','APPROVED_NOT_SCHEDULED')
		  AND d.touchpoint_id IS NOT NULL
		  AND (a.id IS NULL OR a.commercial_qualification_state<>'QUALIFIED'
		    OR d.policy_version NOT IN ($2,$3,$8)
		    OR d.policy_hash<>CASE d.policy_version WHEN $2 THEN $4 WHEN $3 THEN $5 WHEN $8 THEN $9 ELSE '' END
		    OR ($7 AND ($6::uuid IS NULL OR d.policy_authorization_id<>$6))
		    OR $10)
		FOR UPDATE OF d`, orgID,
		DelegatedFirstTouchPolicyV1, DelegatedFirstTouchPolicyV2,
		DelegatedFirstTouchPolicyHashV1, DelegatedFirstTouchPolicyHashV2,
		policyAuthorizationID, checkPolicyAuthorization,
		DelegatedFirstTouchPolicyV3, DelegatedFirstTouchPolicyHashV3,
		authorityRevoked)
	if err != nil {
		return 0, err
	}
	var touchpointIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		touchpointIDs = append(touchpointIDs, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	if len(touchpointIDs) == 0 {
		return 0, tx.Commit(ctx)
	}

	if _, err = tx.Exec(ctx, `
		UPDATE confenge_dispatch_queue q
		SET status='cancelled',cancel_reason=$3,last_error=$3,updated_at=now()
		FROM outreach_touchpoints t
		WHERE t.organization_id=$1 AND t.id=ANY($2::uuid[]) AND t.draft_id=q.draft_id
		  AND q.organization_id=t.organization_id AND q.status IN ('queued','reserved')`,
		orgID, touchpointIDs, delegatedBindingRefreshReason); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE outreach_drafts d
		SET status='NEEDS_REVIEW',approved_by=NULL,approved_at=NULL,
			campaign_id=NULL,enrollment_contact_id=NULL,enrolled_at=NULL,updated_at=now()
		FROM outreach_touchpoints t
		WHERE t.organization_id=$1 AND t.id=ANY($2::uuid[])
		  AND t.draft_id=d.id AND d.organization_id=t.organization_id`, orgID, touchpointIDs); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE outreach_touchpoints
		SET state='NEEDS_REVIEW',approved_content_hash='',approved_by=NULL,approved_at=NULL,
			authorization_mode='',campaign_policy_authorization_id=NULL,authorization_policy_hash='',
			authorization_at=NULL,signature_version='',queued_at=NULL,stop_reason=$3,updated_at=now()
		WHERE organization_id=$1 AND id=ANY($2::uuid[])`, orgID, touchpointIDs, delegatedBindingRefreshReason); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE confenge_delegated_first_touch_decisions
		SET state='CANCELLED',blocker_codes=blocker_codes || jsonb_build_array($3::text),updated_at=now()
		WHERE organization_id=$1 AND touchpoint_id=ANY($2::uuid[])
		  AND state IN ('APPROVED','QUEUED','APPROVED_NOT_SCHEDULED')`,
		orgID, touchpointIDs, delegatedBindingRefreshReason); err != nil {
		return 0, err
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	// Retirement cancels durable queued work, so its blast radius is never silent.
	log.Warn().Str("organization_id", orgID.String()).Int("retired", len(touchpointIDs)).
		Bool("authority_revoked", authorityRevoked).
		Bool("policy_authorization_enforced", checkPolicyAuthorization).
		Msg("confenge: retired delegated first-touch approvals")
	return len(touchpointIDs), nil
}
