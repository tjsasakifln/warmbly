DROP INDEX IF EXISTS outreach_accounts_draft_generation_idx;

CREATE INDEX IF NOT EXISTS outreach_accounts_draft_generation_idx
    ON outreach_accounts (draft_generation_retry_at, created_at, id)
    WHERE queue_state = 'READY_TO_GENERATE'
      AND target_fit_eligible = true
      AND blocked = false
      AND do_not_contact = false;

CREATE INDEX IF NOT EXISTS confenge_delegated_first_touch_review_capacity_idx
    ON confenge_delegated_first_touch_decisions
       (organization_id, account_id, evidence_source_run_id);

CREATE INDEX IF NOT EXISTS outreach_touchpoints_draft_active_account_idx
    ON outreach_touchpoints (organization_id, account_id)
    WHERE state IN (
        'AI_REWRITE_PENDING',
        'ENRICHMENT_PENDING',
        'REJECTED_REWRITE_PENDING',
        'NEEDS_REVIEW',
        'APPROVED',
        'QUEUED'
    );
