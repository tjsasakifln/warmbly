DROP INDEX IF EXISTS confenge_delegated_first_touch_live_content_uidx;

ALTER TABLE confenge_delegated_first_touch_decisions
    DROP CONSTRAINT IF EXISTS confenge_delegated_decision_target_membership_count_check,
    DROP CONSTRAINT IF EXISTS confenge_delegated_decision_target_membership_hash_check,
    DROP CONSTRAINT IF EXISTS confenge_delegated_decision_source_freshness_hash_check,
    DROP COLUMN IF EXISTS target_membership_count,
    DROP COLUMN IF EXISTS target_membership_hash,
    DROP COLUMN IF EXISTS source_freshness_hash,
    DROP COLUMN IF EXISTS source_expires_at;

ALTER TABLE confenge_delegated_first_touch_batches
    DROP CONSTRAINT IF EXISTS confenge_delegated_batch_target_membership_count_check,
    DROP CONSTRAINT IF EXISTS confenge_delegated_batch_target_membership_hash_check,
    DROP CONSTRAINT IF EXISTS confenge_delegated_batch_source_freshness_hash_check,
    DROP COLUMN IF EXISTS target_membership_count,
    DROP COLUMN IF EXISTS target_membership_hash,
    DROP COLUMN IF EXISTS source_freshness_hash,
    DROP COLUMN IF EXISTS source_expires_at;
