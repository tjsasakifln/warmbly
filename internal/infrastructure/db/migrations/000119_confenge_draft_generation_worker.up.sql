-- Durable leasing for READY_TO_GENERATE accounts. This queue ends at human
-- review and grants no approval, scheduling, queueing or transport authority.
ALTER TABLE outreach_accounts
    ADD COLUMN IF NOT EXISTS draft_generation_retry_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN IF NOT EXISTS draft_generation_reserved_until timestamptz,
    ADD COLUMN IF NOT EXISTS draft_generation_attempts int NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS draft_generation_last_error text NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS outreach_accounts_draft_generation_idx
    ON outreach_accounts (draft_generation_retry_at, created_at, id)
    WHERE queue_state = 'READY_TO_GENERATE'
      AND target_fit_eligible = true
      AND email_send_ready = true
      AND blocked = false
      AND do_not_contact = false;

DROP INDEX IF EXISTS outreach_touchpoints_editorial_recovery_idx;
CREATE INDEX outreach_touchpoints_editorial_recovery_idx
    ON outreach_touchpoints (editorial_retry_at, created_at, id)
    WHERE state IN ('AI_REWRITE_PENDING','ENRICHMENT_PENDING','REJECTED_REWRITE_PENDING');
