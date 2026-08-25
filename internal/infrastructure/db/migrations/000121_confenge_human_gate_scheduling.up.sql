-- Individual human-gate approval is the durable send authority. The scheduling
-- row binds one immutable candidate to exactly one touchpoint and makes retries,
-- duplicate clicks and reconciliation converge on one outbound message.
CREATE TABLE IF NOT EXISTS confenge_cohort_candidate_dispatches (
    organization_id uuid NOT NULL,
    cohort_version_id uuid NOT NULL REFERENCES confenge_cohort_versions(id) ON DELETE RESTRICT,
    candidate_id uuid NOT NULL,
    review_id uuid NOT NULL REFERENCES confenge_cohort_candidate_reviews(id) ON DELETE RESTRICT,
    touchpoint_id uuid NOT NULL REFERENCES outreach_touchpoints(id) ON DELETE RESTRICT,
    draft_id uuid NOT NULL REFERENCES outreach_drafts(id) ON DELETE RESTRICT,
    auto_send boolean NOT NULL DEFAULT true,
    due_at timestamptz NOT NULL,
    scheduled_at timestamptz NOT NULL DEFAULT now(),
    invalidated_at timestamptz,
    invalidation_reason text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (cohort_version_id, candidate_id),
    CONSTRAINT confenge_cohort_candidate_dispatches_auto_send_check CHECK (auto_send = true)
);

CREATE INDEX IF NOT EXISTS confenge_cohort_candidate_dispatches_org_due_idx
    ON confenge_cohort_candidate_dispatches (organization_id, due_at)
    WHERE invalidated_at IS NULL;

ALTER TABLE outreach_touchpoints
    DROP CONSTRAINT IF EXISTS outreach_touchpoints_auth_mode_check;

ALTER TABLE outreach_touchpoints
    ADD CONSTRAINT outreach_touchpoints_auth_mode_check
        CHECK (authorization_mode IN (
            '', 'HUMAN_TOUCHPOINT_APPROVAL', 'HUMAN_GATE_APPROVAL',
            'CAMPAIGN_POLICY', 'BOUNDED_COHORT_AUTHORIZATION'
        ));

COMMENT ON TABLE confenge_cohort_candidate_dispatches IS
'One immutable human-gate APPROVE maps to one idempotent queued message. auto_send=true is per approved message, not the prohibited global automation flag.';
