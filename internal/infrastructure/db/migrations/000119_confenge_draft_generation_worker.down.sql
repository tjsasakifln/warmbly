DROP INDEX IF EXISTS outreach_accounts_draft_generation_idx;
ALTER TABLE outreach_accounts
    DROP COLUMN IF EXISTS draft_generation_last_error,
    DROP COLUMN IF EXISTS draft_generation_attempts,
    DROP COLUMN IF EXISTS draft_generation_reserved_until,
    DROP COLUMN IF EXISTS draft_generation_retry_at;

DROP INDEX IF EXISTS outreach_touchpoints_editorial_recovery_idx;
CREATE INDEX outreach_touchpoints_editorial_recovery_idx
    ON outreach_touchpoints (editorial_retry_at, created_at)
    WHERE state IN ('AI_REWRITE_PENDING','REJECTED_REWRITE_PENDING');
