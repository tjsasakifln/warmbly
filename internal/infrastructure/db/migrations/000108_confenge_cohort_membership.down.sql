ALTER TABLE confenge_bounded_cohort_authorizations
    DROP COLUMN IF EXISTS frozen_manifest,
    DROP COLUMN IF EXISTS go_review_verdict,
    DROP COLUMN IF EXISTS go_review_actor,
    DROP COLUMN IF EXISTS go_review_at,
    DROP COLUMN IF EXISTS go_review_reason;
