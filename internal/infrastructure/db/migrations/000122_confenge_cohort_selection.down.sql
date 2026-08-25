DROP TABLE IF EXISTS confenge_cohort_selection_claims;

ALTER TABLE confenge_cohort_versions
    DROP CONSTRAINT IF EXISTS confenge_cohort_versions_selection_mode_check;

ALTER TABLE confenge_cohort_versions
    DROP COLUMN IF EXISTS selection_report,
    DROP COLUMN IF EXISTS selection_mode;
