DROP INDEX IF EXISTS confenge_dispatch_sends_queue_idx;
DROP INDEX IF EXISTS confenge_dispatch_sends_provider_msg_idx;

-- Dropping these columns discards observed provider facts. The send rows
-- themselves are never removed: acceptance that happened stays recorded.
ALTER TABLE confenge_dispatch_sends
    DROP COLUMN IF EXISTS queue_id,
    DROP COLUMN IF EXISTS provider_message_id,
    DROP COLUMN IF EXISTS provider,
    DROP COLUMN IF EXISTS recipient;
