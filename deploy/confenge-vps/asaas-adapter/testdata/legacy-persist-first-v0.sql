-- Regression model for the persist-first Asaas queue observed live in issue #186.
-- The metadata table is present but empty, so the schema remains unversioned.
CREATE TABLE metadata (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE events (
    provider_event_id TEXT PRIMARY KEY,
    received_at INTEGER NOT NULL,
    raw_type TEXT NOT NULL,
    payload_json TEXT NOT NULL,
    payload_sha256 TEXT NOT NULL,
    status TEXT NOT NULL,
    attempts INTEGER NOT NULL,
    next_attempt_at INTEGER NOT NULL,
    last_error TEXT NOT NULL,
    canonical_json TEXT NOT NULL
);

INSERT INTO events (
    provider_event_id,
    received_at,
    raw_type,
    payload_json,
    payload_sha256,
    status,
    attempts,
    next_attempt_at,
    last_error,
    canonical_json
) VALUES (
    'evt_fixture_persist_first',
    1787745600,
    'PAYMENT_CREATED',
    '{}',
    '44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a',
    'pending',
    0,
    0,
    '',
    '{}'
);
