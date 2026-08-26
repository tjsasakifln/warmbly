DROP INDEX IF EXISTS outreach_import_runs_org_source_run_snapshot_idx;
DROP INDEX IF EXISTS outreach_touchpoints_delegated_prepared_idx;
DROP INDEX IF EXISTS outreach_touchpoints_org_account_run_initial_uidx;

ALTER TABLE outreach_touchpoints
    DROP COLUMN IF EXISTS delegated_last_error,
    DROP COLUMN IF EXISTS delegated_attempts,
    DROP COLUMN IF EXISTS delegated_retry_at,
    DROP COLUMN IF EXISTS delegated_reserved_until,
    DROP COLUMN IF EXISTS source_run_id;

ALTER TABLE outreach_accounts
    DROP COLUMN IF EXISTS initial_backlog_reason_code;
