-- The CONFENGE first-touch send ledger recorded that a message was accepted but
-- not what was accepted, by whom, or under which provider id. Reconciling an
-- ambiguous provider result therefore had nothing to correlate against, and the
-- only recipient on record lived on the queue row, which is mutable.
--
-- These columns make confenge_dispatch_sends self-contained external truth:
-- one row states recipient, provider, provider message id and the queue row it
-- discharged. Duplicate prevention stays where it already was -- the existing
-- UNIQUE (message_key) -- so this adds evidence, never a second authority.

ALTER TABLE confenge_dispatch_sends
    ADD COLUMN IF NOT EXISTS recipient           text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS provider            text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS provider_message_id text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS queue_id            uuid;

-- Correlation index for ambiguous-result reconciliation: given a provider id
-- observed later (bounce, DSN, mailbox sync), find the send that produced it.
CREATE INDEX IF NOT EXISTS confenge_dispatch_sends_provider_msg_idx
    ON confenge_dispatch_sends (organization_id, provider_message_id)
    WHERE provider_message_id <> '';

-- Answering "was this queue row already sent?" without joining on the mutable
-- message_key text.
CREATE INDEX IF NOT EXISTS confenge_dispatch_sends_queue_idx
    ON confenge_dispatch_sends (queue_id)
    WHERE queue_id IS NOT NULL;
