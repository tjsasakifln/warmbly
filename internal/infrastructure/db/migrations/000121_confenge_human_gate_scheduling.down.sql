-- A binary rollback must not leave queue work whose authorization mode the old
-- binary cannot revalidate. Cancel unsent items and clear their approval first.
UPDATE confenge_dispatch_queue q
SET status='cancelled', cancel_reason='human_gate_feature_rollback',
    last_error='human_gate_feature_rollback', updated_at=now()
FROM outreach_touchpoints t
WHERE q.organization_id=t.organization_id AND q.draft_id=t.draft_id
  AND t.authorization_mode='HUMAN_GATE_APPROVAL'
  AND q.status IN ('queued','reserved','failed');

UPDATE outreach_touchpoints t
SET state=CASE WHEN t.state IN ('APPROVED','QUEUED') THEN 'NEEDS_REVIEW' ELSE t.state END,
    approved_by=NULL, approved_at=NULL, approved_content_hash='',
    authorization_mode='', campaign_policy_authorization_id=NULL,
    authorization_policy_hash='', authorization_at=NULL,
    stop_reason=CASE WHEN t.state IN ('APPROVED','QUEUED') THEN 'human_gate_feature_rollback' ELSE t.stop_reason END,
    updated_at=now()
WHERE t.authorization_mode='HUMAN_GATE_APPROVAL' AND t.state <> 'SENT';

-- Sent history is immutable. Reclassify only its audit label to a mode the old
-- schema understands; it can never re-enter the queue from SENT.
UPDATE outreach_touchpoints t
SET authorization_mode='HUMAN_TOUCHPOINT_APPROVAL', updated_at=now()
WHERE t.authorization_mode='HUMAN_GATE_APPROVAL' AND t.state='SENT';

DROP TABLE IF EXISTS confenge_cohort_candidate_dispatches;

ALTER TABLE outreach_touchpoints
    DROP CONSTRAINT IF EXISTS outreach_touchpoints_auth_mode_check;

ALTER TABLE outreach_touchpoints
    ADD CONSTRAINT outreach_touchpoints_auth_mode_check
        CHECK (authorization_mode IN (
            '', 'HUMAN_TOUCHPOINT_APPROVAL', 'CAMPAIGN_POLICY',
            'BOUNDED_COHORT_AUTHORIZATION'
        ));
