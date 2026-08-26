ALTER TABLE outreach_accounts
    ADD COLUMN IF NOT EXISTS initial_backlog_reason_code text NOT NULL DEFAULT '';

ALTER TABLE outreach_touchpoints
    ADD COLUMN IF NOT EXISTS source_run_id text NOT NULL DEFAULT '';

CREATE UNIQUE INDEX IF NOT EXISTS outreach_touchpoints_org_account_run_initial_uidx
    ON outreach_touchpoints (organization_id, account_id, source_run_id)
    WHERE source_run_id <> '' AND ordinal = 1 AND purpose = 'INITIAL' AND channel = 'EMAIL';

CREATE INDEX IF NOT EXISTS outreach_touchpoints_current_initial_idx
    ON outreach_touchpoints (organization_id, source_run_id, due_at, id)
    WHERE ordinal = 1 AND purpose = 'INITIAL' AND channel = 'EMAIL'
      AND state IN ('DUE', 'NEEDS_REVIEW');
