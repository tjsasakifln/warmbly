-- The durable INTEL_WATCH opportunity-event inbox.
--
-- This closes the replayability boundary. Dedup (confenge_intel_watch_dedup)
-- buys at-most-once and is source-independent. Automatic reprocessing of a
-- PENDING delivery is a different property: it needs the event to still exist
-- somewhere, and the ledger deliberately stores delivery identity, not content.
-- The real upstream posts once and cannot be asked to post again, so Warmbly
-- persists the envelope here at the moment of ingestion and replays from this
-- table instead of from the caller.
--
-- This is an inbox, not a second ledger. It is append-only inside a bounded
-- replay window: a row is never marked consumed by the act of being emitted,
-- because a consumer that fails transiently right after emission would
-- otherwise lose the only copy of the event. Re-emission is free: the dedup
-- ledger refuses a duplicate by primary key. emit_lease_until bounds duplicate
-- WORK between two producer instances; it is not a correctness boundary.

CREATE TABLE IF NOT EXISTS confenge_intel_watch_inbox (
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    event_id         text NOT NULL,
    event_schema     text NOT NULL,
    event_type       text NOT NULL,
    subject_key      text NOT NULL,
    occurred_at      timestamptz NOT NULL,
    payload          jsonb NOT NULL,
    received_at      timestamptz NOT NULL DEFAULT now(),
    emit_lease_until timestamptz,
    emitted_count    integer NOT NULL DEFAULT 0,
    last_emitted_at  timestamptz,
    -- The organization comes from Warmbly's own webhook auth, never from the
    -- payload, so it is part of the identity rather than a column beside it.
    PRIMARY KEY (organization_id, event_id),
    CHECK (event_id <> ''),
    CHECK (subject_key <> ''),
    CHECK (event_schema = 'CONFENGE_OPPORTUNITY_EVENT/1.0'),
    CHECK (event_type IN ('NEW_OPPORTUNITY', 'OPPORTUNITY_CHANGED', 'DEADLINE_CHANGED', 'FIT_BECAME_RELEVANT')),
    CHECK (jsonb_typeof(payload) = 'object')
);

-- The producer's only read path: rows still inside the replay window whose
-- emit lease is free.
CREATE INDEX IF NOT EXISTS confenge_intel_watch_inbox_replay_idx
    ON confenge_intel_watch_inbox (received_at DESC, emit_lease_until);

COMMENT ON TABLE confenge_intel_watch_inbox IS
'Durable inbound opportunity-event envelopes. Append-only inside a bounded replay window; emission never consumes a row. Replaces the fixture file as the production EventProducer source.';

-- The INTEL_SEED send ledger.
--
-- INTEL_SEED takes no dispatch reservation and no queue row, so before this
-- table there was nothing that counted an INTEL_SEED send. Its daily cap needs
-- its OWN counter: a cap carved out of the first-touch governor would be a
-- reduction of first touch's budget dressed up as a new lane. This table is
-- that counter, and it doubles as the idempotency record for one seed touch.
CREATE TABLE IF NOT EXISTS confenge_intel_seed_sends (
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    message_key     text NOT NULL,
    candidate_id    uuid REFERENCES outreach_contact_candidates(id) ON DELETE SET NULL,
    account_id      uuid REFERENCES outreach_accounts(id) ON DELETE SET NULL,
    recipient       text NOT NULL,
    subject_key     text NOT NULL DEFAULT '',
    sent_at         timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, message_key),
    CHECK (message_key <> ''),
    CHECK (recipient <> '')
);

-- The cap's only read path: this organization's sends since the day boundary.
CREATE INDEX IF NOT EXISTS confenge_intel_seed_sends_daily_idx
    ON confenge_intel_seed_sends (organization_id, sent_at DESC);

-- Reply attribution reads this: which lane last wrote to this address.
CREATE INDEX IF NOT EXISTS confenge_intel_seed_sends_recipient_idx
    ON confenge_intel_seed_sends (organization_id, recipient, sent_at DESC);
