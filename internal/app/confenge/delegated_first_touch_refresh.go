package confenge

import (
	"context"

	"github.com/google/uuid"
)

const delegatedBindingRefreshReason = "delegated_authority_or_source_binding_advanced"

// retireStaleDelegatedFirstTouches revokes approvals whose account is no longer
// commercially QUALIFIED, or whose runtime, policy or membership binding moved.
// Source run id, snapshot expiry and freshness hash are acquisition provenance
// and never retire a still-qualified decision.
func (s *service) retireStaleDelegatedFirstTouches(ctx context.Context, orgID uuid.UUID, sourceRunID, snapshotHash string, policyAuthorizationIDs ...*uuid.UUID) (int, error) {
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
	authorityValid := feedErr == nil && feedState != nil && feedState.LastRunID == sourceRunID &&
		feedState.LastSnapshotHash == snapshotHash &&
		validateAuthoritativeFeedStructure(feedState, true) == nil &&
		FeedCommercialAuthorityState(feedState).State == CommercialQualified
	targetMembershipHash, targetMembershipCount := "", 0
	if feedState != nil {
		targetMembershipHash = feedState.TargetMembershipHash
		targetMembershipCount = feedState.TargetMembershipCount
	}

	rows, err := tx.Query(ctx, `
		SELECT d.touchpoint_id
		FROM confenge_delegated_first_touch_decisions d
		LEFT JOIN outreach_accounts a
		  ON a.organization_id=d.organization_id AND a.id=d.account_id
		WHERE d.organization_id=$1
		  AND d.state IN ('APPROVED','QUEUED','APPROVED_NOT_SCHEDULED')
		  AND d.touchpoint_id IS NOT NULL
		  AND (a.id IS NULL OR a.commercial_qualification_state<>'QUALIFIED'
		    OR d.runtime_release_sha<>$2
		    OR d.policy_version NOT IN ($3,$4,$12)
		    OR d.policy_hash<>CASE d.policy_version WHEN $3 THEN $5 WHEN $4 THEN $6 WHEN $12 THEN $13 ELSE '' END
		    OR ($8 AND ($7::uuid IS NULL OR d.policy_authorization_id<>$7))
		    OR NOT $9 OR d.target_membership_hash<>$10
		    OR d.target_membership_count<>$11)
		FOR UPDATE OF d`, orgID, s.cfg.RepositorySHA,
		DelegatedFirstTouchPolicyV1, DelegatedFirstTouchPolicyV2,
		DelegatedFirstTouchPolicyHashV1, DelegatedFirstTouchPolicyHashV2,
		policyAuthorizationID, checkPolicyAuthorization,
		authorityValid, targetMembershipHash, targetMembershipCount,
		DelegatedFirstTouchPolicyV3, DelegatedFirstTouchPolicyHashV3)
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
	return len(touchpointIDs), nil
}
