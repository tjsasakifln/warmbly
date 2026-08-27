-- Additive pause provenance and commercial-authority persistence.
ALTER TABLE confenge_dispatch_control
    ADD COLUMN IF NOT EXISTS pause_source text NOT NULL DEFAULT '';

ALTER TABLE confenge_dispatch_control
    DROP CONSTRAINT IF EXISTS confenge_dispatch_control_pause_source_check;
ALTER TABLE confenge_dispatch_control
    ADD CONSTRAINT confenge_dispatch_control_pause_source_check
        CHECK (pause_source IN ('', 'api', 'kill_switch_file', 'worker_guard', 'environment', 'durable_control', 'configuration'));

COMMENT ON COLUMN confenge_dispatch_control.pause_source IS
'Durable pause path. File-only pauses are not stored here; they appear on dispatch status as kill_switch_file.';

ALTER TABLE outreach_feed_sync_state
    ADD COLUMN IF NOT EXISTS commercial_authority jsonb;

COMMENT ON COLUMN outreach_feed_sync_state.commercial_authority IS
'Additive extra-cli commercial_authority payload. NULL means the current fail-closed source-freshness path.';
