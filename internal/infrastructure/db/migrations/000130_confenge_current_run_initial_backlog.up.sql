ALTER TABLE outreach_accounts
    ADD COLUMN IF NOT EXISTS initial_backlog_reason_code text NOT NULL DEFAULT '';

ALTER TABLE outreach_touchpoints
    ADD COLUMN IF NOT EXISTS source_run_id text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS delegated_reserved_until timestamptz,
    ADD COLUMN IF NOT EXISTS delegated_retry_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN IF NOT EXISTS delegated_attempts int NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS delegated_last_error text NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS outreach_touchpoints_org_account_run_initial_uidx
    ON outreach_touchpoints (organization_id, account_id, source_run_id)
    WHERE source_run_id <> '' AND ordinal = 1 AND purpose = 'INITIAL' AND channel = 'EMAIL';

CREATE INDEX IF NOT EXISTS outreach_touchpoints_delegated_prepared_idx
    ON outreach_touchpoints (organization_id, source_run_id, delegated_retry_at, due_at, id)
    WHERE ordinal = 1 AND purpose = 'INITIAL' AND channel = 'EMAIL'
      AND state IN ('DUE', 'NEEDS_REVIEW');

CREATE INDEX IF NOT EXISTS outreach_import_runs_org_source_run_snapshot_idx
    ON outreach_import_runs (organization_id, source_run_id, snapshot_hash)
    WHERE source_run_id <> '' AND snapshot_hash <> '';
