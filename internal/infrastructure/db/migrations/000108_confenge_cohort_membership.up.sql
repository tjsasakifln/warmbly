-- Frozen membership + human GO review on the existing bounded cohort grant.
-- Membership is an immutable JSON snapshot of exact account/candidate/mailbox
-- /route_class/content. GO review is a later human decision on that grant.

ALTER TABLE confenge_bounded_cohort_authorizations
    ADD COLUMN IF NOT EXISTS frozen_manifest jsonb NOT NULL DEFAULT 'null'::jsonb,
    ADD COLUMN IF NOT EXISTS go_review_verdict text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS go_review_actor uuid,
    ADD COLUMN IF NOT EXISTS go_review_at timestamptz,
    ADD COLUMN IF NOT EXISTS go_review_reason text NOT NULL DEFAULT '';

COMMENT ON COLUMN confenge_bounded_cohort_authorizations.frozen_manifest IS
'Exact frozen cohort membership. Reconstructs authorization_id → account → candidate → mailbox → route_class → content hash.';
COMMENT ON COLUMN confenge_bounded_cohort_authorizations.go_review_verdict IS
'Human GO review: READY_FOR_CONTROLLED_EMAIL_GO_REVIEW or NO_GO. Never live GO_FOR_CONTROLLED_EMAIL_PILOT.';
