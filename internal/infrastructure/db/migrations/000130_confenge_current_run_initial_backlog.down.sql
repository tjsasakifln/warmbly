DROP INDEX IF EXISTS outreach_touchpoints_current_initial_idx;
DROP INDEX IF EXISTS outreach_touchpoints_org_account_run_initial_uidx;

ALTER TABLE outreach_touchpoints DROP COLUMN IF EXISTS source_run_id;
ALTER TABLE outreach_accounts DROP COLUMN IF EXISTS initial_backlog_reason_code;
