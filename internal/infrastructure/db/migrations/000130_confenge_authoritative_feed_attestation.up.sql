-- Persist the producer-owned source expiry and exact TARGET_CONFIRMED membership.

ALTER TABLE outreach_feed_sync_state
    ADD COLUMN IF NOT EXISTS source_expires_at timestamptz,
    ADD COLUMN IF NOT EXISTS source_freshness_hash text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS target_membership_complete boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS target_membership_hash text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS target_membership_count integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS supplier_confirmed_count integer NOT NULL DEFAULT 0;

ALTER TABLE outreach_feed_sync_state
    ADD CONSTRAINT outreach_feed_sync_freshness_hash_check
        CHECK (source_freshness_hash = '' OR source_freshness_hash ~ '^[a-f0-9]{64}$'),
    ADD CONSTRAINT outreach_feed_sync_membership_hash_check
        CHECK (target_membership_hash = '' OR target_membership_hash ~ '^[a-f0-9]{64}$'),
    ADD CONSTRAINT outreach_feed_sync_membership_count_check
        CHECK (target_membership_count >= 0),
    ADD CONSTRAINT outreach_feed_sync_supplier_count_check
        CHECK (supplier_confirmed_count >= 0 AND supplier_confirmed_count <= target_membership_count);

COMMENT ON COLUMN outreach_feed_sync_state.source_expires_at IS
'Producer-owned authoritative source expiry. It overrides the generic feed max age when delegated first-touch is enabled.';
COMMENT ON COLUMN outreach_feed_sync_state.target_membership_hash IS
'Producer-owned SHA-256 of the sorted unique TARGET_CONFIRMED CNPJ roots.';
