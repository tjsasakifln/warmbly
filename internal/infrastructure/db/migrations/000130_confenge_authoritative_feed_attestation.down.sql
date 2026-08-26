ALTER TABLE outreach_feed_sync_state
    DROP CONSTRAINT IF EXISTS outreach_feed_sync_supplier_count_check,
    DROP CONSTRAINT IF EXISTS outreach_feed_sync_membership_count_check,
    DROP CONSTRAINT IF EXISTS outreach_feed_sync_membership_hash_check,
    DROP CONSTRAINT IF EXISTS outreach_feed_sync_freshness_hash_check,
    DROP COLUMN IF EXISTS supplier_confirmed_count,
    DROP COLUMN IF EXISTS target_membership_count,
    DROP COLUMN IF EXISTS target_membership_hash,
    DROP COLUMN IF EXISTS target_membership_complete,
    DROP COLUMN IF EXISTS source_freshness_hash,
    DROP COLUMN IF EXISTS source_expires_at;
