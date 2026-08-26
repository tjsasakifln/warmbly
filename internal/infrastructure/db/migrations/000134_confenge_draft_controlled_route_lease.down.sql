DROP INDEX IF EXISTS outreach_touchpoints_draft_active_account_idx;
DROP INDEX IF EXISTS confenge_delegated_first_touch_review_capacity_idx;
DROP INDEX IF EXISTS outreach_accounts_draft_generation_idx;

CREATE INDEX outreach_accounts_draft_generation_idx
    ON outreach_accounts (draft_generation_retry_at, created_at, id)
    WHERE queue_state = 'READY_TO_GENERATE'
      AND target_fit_eligible = true
      AND email_send_ready = true
      AND blocked = false
      AND do_not_contact = false;
