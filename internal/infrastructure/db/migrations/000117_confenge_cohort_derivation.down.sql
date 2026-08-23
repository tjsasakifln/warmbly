DROP INDEX IF EXISTS confenge_cohort_adjustments_from_idx;
DROP INDEX IF EXISTS confenge_cohort_adjustments_cohort_idx;
DROP TABLE IF EXISTS confenge_cohort_adjustments;

ALTER TABLE confenge_cohort_versions
    DROP CONSTRAINT IF EXISTS confenge_cohort_versions_parent_check;

ALTER TABLE confenge_cohort_versions
    DROP CONSTRAINT IF EXISTS confenge_cohort_versions_derivation_check;

ALTER TABLE confenge_cohort_versions
    DROP COLUMN IF EXISTS parent_version;

ALTER TABLE confenge_cohort_versions
    DROP COLUMN IF EXISTS derivation;
