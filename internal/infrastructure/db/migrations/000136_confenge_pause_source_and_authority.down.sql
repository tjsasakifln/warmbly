ALTER TABLE outreach_feed_sync_state
    DROP COLUMN IF EXISTS commercial_authority;

ALTER TABLE confenge_dispatch_control
    DROP CONSTRAINT IF EXISTS confenge_dispatch_control_pause_source_check;
ALTER TABLE confenge_dispatch_control
    DROP COLUMN IF EXISTS pause_source;
