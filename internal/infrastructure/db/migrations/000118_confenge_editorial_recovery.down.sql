DROP INDEX IF EXISTS confenge_dispatch_queue_reclaim_idx;
ALTER TABLE confenge_dispatch_queue DROP COLUMN IF EXISTS attempts;
ALTER TABLE confenge_dispatch_queue DROP COLUMN IF EXISTS reserved_until;

DROP TABLE IF EXISTS confenge_editorial_issue_outbox;
DROP TABLE IF EXISTS confenge_editorial_guideline_sets;
DROP TABLE IF EXISTS confenge_editorial_signals;

DROP INDEX IF EXISTS outreach_touchpoints_editorial_recovery_idx;
ALTER TABLE outreach_touchpoints DROP COLUMN IF EXISTS editorial_attempts;
ALTER TABLE outreach_touchpoints DROP COLUMN IF EXISTS editorial_reserved_until;
ALTER TABLE outreach_touchpoints DROP COLUMN IF EXISTS editorial_retry_at;

DROP INDEX IF EXISTS outreach_touchpoints_org_review_idx;
ALTER TABLE outreach_touchpoints DROP CONSTRAINT IF EXISTS outreach_touchpoints_state_check;
ALTER TABLE outreach_touchpoints ADD CONSTRAINT outreach_touchpoints_state_check CHECK (state IN (
    'PLANNED','DUE','DRAFTED','NEEDS_REVIEW','APPROVED','QUEUED','SENT',
    'SKIPPED','REJECTED','REPLIED','DNC','BOUNCED','CANCELLED','FAILED'));
CREATE INDEX outreach_touchpoints_org_review_idx ON outreach_touchpoints (organization_id, state)
    WHERE state IN ('DUE','DRAFTED','NEEDS_REVIEW','APPROVED');

DROP INDEX IF EXISTS outreach_drafts_org_account_active_uidx;
ALTER TABLE outreach_drafts DROP CONSTRAINT IF EXISTS outreach_drafts_status_check;
ALTER TABLE outreach_drafts ADD CONSTRAINT outreach_drafts_status_check CHECK (status IN (
    'NOT_GENERATED','GENERATING','NEEDS_REVIEW','APPROVED','REJECTED','SKIPPED',
    'BLOCKED','ENROLLED','SENT','REPLIED'));
CREATE UNIQUE INDEX outreach_drafts_org_account_active_uidx
    ON outreach_drafts (organization_id, account_id)
    WHERE status IN ('NOT_GENERATED','GENERATING','NEEDS_REVIEW','APPROVED');
