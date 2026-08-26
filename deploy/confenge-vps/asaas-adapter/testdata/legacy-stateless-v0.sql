-- Regression model for the unversioned Asaas queue reported in issue #186.
-- It deliberately has no events.state column or schema-version metadata.
CREATE TABLE events (
    provider_event_id TEXT PRIMARY KEY,
    correlation_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    occurred_at TEXT NOT NULL,
    received_at TEXT NOT NULL,
    payload TEXT NOT NULL,
    payload_sha256 TEXT NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at REAL NOT NULL DEFAULT 0,
    lease_until REAL,
    last_http_status INTEGER,
    last_code TEXT,
    updated_at TEXT NOT NULL,
    processed_at TEXT
);

INSERT INTO events (
    provider_event_id,
    correlation_id,
    event_type,
    occurred_at,
    received_at,
    payload,
    payload_sha256,
    attempts,
    next_attempt_at,
    updated_at
) VALUES (
    'evt_fixture_legacy',
    'corr_fixture_legacy',
    'PAYMENT_CREATED',
    '2026-08-25T00:00:00Z',
    '2026-08-25T00:00:00Z',
    '{}',
    '44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a',
    0,
    0,
    '2026-08-25T00:00:00Z'
);
