-- Recoverable editorial states. A lead is never discarded merely because its
-- current copy or evidence is incomplete.
ALTER TABLE outreach_drafts DROP CONSTRAINT IF EXISTS outreach_drafts_status_check;
ALTER TABLE outreach_drafts ADD CONSTRAINT outreach_drafts_status_check
    CHECK (status IN (
        'NOT_GENERATED','GENERATING','AI_REWRITE_PENDING','ENRICHMENT_PENDING',
        'REJECTED_REWRITE_PENDING','NEEDS_REVIEW','APPROVED','REJECTED',
        'SKIPPED','BLOCKED','ENROLLED','SENT','REPLIED'
    ));

DROP INDEX IF EXISTS outreach_drafts_org_account_active_uidx;
CREATE UNIQUE INDEX outreach_drafts_org_account_active_uidx
    ON outreach_drafts (organization_id, account_id)
    WHERE status IN (
        'NOT_GENERATED','GENERATING','AI_REWRITE_PENDING','ENRICHMENT_PENDING',
        'REJECTED_REWRITE_PENDING','NEEDS_REVIEW','APPROVED'
    );

ALTER TABLE outreach_touchpoints DROP CONSTRAINT IF EXISTS outreach_touchpoints_state_check;
ALTER TABLE outreach_touchpoints ADD CONSTRAINT outreach_touchpoints_state_check
    CHECK (state IN (
        'PLANNED','DUE','DRAFTED','AI_REWRITE_PENDING','ENRICHMENT_PENDING',
        'REJECTED_REWRITE_PENDING','NEEDS_REVIEW','APPROVED','QUEUED','SENT',
        'SKIPPED','REJECTED','REPLIED','DNC','BOUNCED','CANCELLED','FAILED'
    ));

ALTER TABLE outreach_touchpoints
    ADD COLUMN IF NOT EXISTS editorial_retry_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN IF NOT EXISTS editorial_reserved_until timestamptz,
    ADD COLUMN IF NOT EXISTS editorial_attempts int NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS outreach_touchpoints_editorial_recovery_idx
    ON outreach_touchpoints (editorial_retry_at, created_at)
    WHERE state IN ('AI_REWRITE_PENDING','REJECTED_REWRITE_PENDING');

DROP INDEX IF EXISTS outreach_touchpoints_org_review_idx;
CREATE INDEX outreach_touchpoints_org_review_idx ON outreach_touchpoints (organization_id, state)
    WHERE state IN (
        'DUE','DRAFTED','AI_REWRITE_PENDING','ENRICHMENT_PENDING',
        'REJECTED_REWRITE_PENDING','NEEDS_REVIEW','APPROVED'
    );

CREATE TABLE IF NOT EXISTS confenge_editorial_signals (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    draft_id            uuid REFERENCES outreach_drafts(id) ON DELETE SET NULL,
    touchpoint_id       uuid REFERENCES outreach_touchpoints(id) ON DELETE SET NULL,
    signal_kind         text NOT NULL,
    reason_code         text NOT NULL,
    defect_signature    text NOT NULL,
    composer_version    text NOT NULL DEFAULT '',
    prompt_version      text NOT NULL DEFAULT '',
    guideline_version   text NOT NULL DEFAULT 'confenge.editorial-guidelines.v1',
    redacted_example    text NOT NULL DEFAULT '',
    occurrences         int NOT NULL DEFAULT 1,
    first_seen_at       timestamptz NOT NULL DEFAULT now(),
    last_seen_at        timestamptz NOT NULL DEFAULT now(),
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, defect_signature)
);

CREATE INDEX IF NOT EXISTS confenge_editorial_signals_pending_idx
    ON confenge_editorial_signals (last_seen_at DESC, occurrences DESC);

CREATE TABLE IF NOT EXISTS confenge_editorial_guideline_sets (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    version             text NOT NULL,
    status              text NOT NULL DEFAULT 'PROPOSED'
        CHECK (status IN ('PROPOSED','ACTIVE','REJECTED','SUPERSEDED')),
    rules_json          jsonb NOT NULL DEFAULT '[]'::jsonb,
    source_signal_ids   jsonb NOT NULL DEFAULT '[]'::jsonb,
    proposed_by         uuid REFERENCES users(id) ON DELETE SET NULL,
    reviewed_by         uuid REFERENCES users(id) ON DELETE SET NULL,
    reviewed_at         timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, version)
);

CREATE UNIQUE INDEX IF NOT EXISTS confenge_editorial_guideline_active_uidx
    ON confenge_editorial_guideline_sets (organization_id) WHERE status = 'ACTIVE';

CREATE TABLE IF NOT EXISTS confenge_editorial_issue_outbox (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    defect_signature    text NOT NULL,
    repository          text NOT NULL DEFAULT 'tjsasakifln/Warmbly',
    title               text NOT NULL,
    body_redacted       text NOT NULL,
    labels              jsonb NOT NULL DEFAULT '["editorial-quality","coding-agent"]'::jsonb,
    status              text NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING','PUBLISHED','FAILED')),
    github_issue_number bigint,
    github_issue_url    text NOT NULL DEFAULT '',
    attempts            int NOT NULL DEFAULT 0,
    next_attempt_at     timestamptz NOT NULL DEFAULT now(),
    last_error          text NOT NULL DEFAULT '',
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, defect_signature)
);

ALTER TABLE confenge_dispatch_queue
    ADD COLUMN IF NOT EXISTS reserved_until timestamptz,
    ADD COLUMN IF NOT EXISTS attempts int NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS confenge_dispatch_queue_reclaim_idx
    ON confenge_dispatch_queue (reserved_until)
    WHERE status = 'reserved';
