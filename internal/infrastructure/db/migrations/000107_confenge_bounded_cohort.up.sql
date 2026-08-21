-- BOUNDED_COHORT_AUTHORIZATION: durable grant + daily-slot authority.
-- Additive and reversible. No secrets. organization_id/actor_id are audit
-- identifiers (no FK) so a grant survives org-row churn and remains
-- restart-safe across backend and consumer processes.

CREATE TABLE IF NOT EXISTS confenge_bounded_cohort_authorizations (
    id                      uuid PRIMARY KEY,
    organization_id         uuid NOT NULL,
    actor_id                uuid NOT NULL,
    authorized_at           timestamptz NOT NULL,
    repository_sha          text NOT NULL,
    feed_schema_version     text NOT NULL,
    cohort_id               text NOT NULL,
    cohort_hash             text NOT NULL,
    policy_version          text NOT NULL,
    allowed_route_classes   text[] NOT NULL DEFAULT '{}',
    max_daily_volume        int NOT NULL,
    recipient_set_hash      text NOT NULL,
    composer_version        text NOT NULL,
    evidence_version        text NOT NULL,
    ttl_seconds             bigint NOT NULL DEFAULT 0,
    expires_at              timestamptz NOT NULL,
    frozen_hash             text NOT NULL,
    revoked_at              timestamptz,
    revoke_actor            uuid,
    revoke_reason           text NOT NULL DEFAULT '',
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT confenge_bca_volume_check CHECK (max_daily_volume >= 1 AND max_daily_volume <= 200),
    CONSTRAINT confenge_bca_sha_check CHECK (repository_sha <> ''),
    CONSTRAINT confenge_bca_hash_check CHECK (frozen_hash <> '')
);

CREATE INDEX IF NOT EXISTS confenge_bca_org_active_idx
    ON confenge_bounded_cohort_authorizations (organization_id, authorized_at DESC)
    WHERE revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS confenge_bca_cohort_hash_idx
    ON confenge_bounded_cohort_authorizations (cohort_hash);

-- Per-message reservation: RESERVED consumes capacity before transport;
-- SENT is terminal after provider accept; RELEASED frees a pre-transport slot.
-- UNIQUE (authorization_id, message_key) makes replay idempotent.
CREATE TABLE IF NOT EXISTS confenge_bounded_cohort_reservations (
    authorization_id    uuid NOT NULL REFERENCES confenge_bounded_cohort_authorizations(id) ON DELETE CASCADE,
    message_key         text NOT NULL,
    day                 date NOT NULL,
    state               text NOT NULL,
    reserved_at         timestamptz NOT NULL DEFAULT now(),
    committed_at        timestamptz,
    released_at         timestamptz,
    release_reason      text NOT NULL DEFAULT '',
    lease_token         text NOT NULL DEFAULT '',
    PRIMARY KEY (authorization_id, message_key),
    CONSTRAINT confenge_bcr_state_check CHECK (state IN ('reserved', 'sent', 'released'))
);

CREATE INDEX IF NOT EXISTS confenge_bcr_day_active_idx
    ON confenge_bounded_cohort_reservations (authorization_id, day)
    WHERE state IN ('reserved', 'sent');

CREATE INDEX IF NOT EXISTS confenge_bcr_lease_token_idx
    ON confenge_bounded_cohort_reservations (lease_token)
    WHERE lease_token <> '' AND state = 'reserved';

COMMENT ON TABLE confenge_bounded_cohort_authorizations IS
'Durable BOUNDED_COHORT_AUTHORIZATION grant. Backend and consumer share this authority. Memory store is tests-only.';
COMMENT ON TABLE confenge_bounded_cohort_reservations IS
'Atomic daily-cap slots. Occupied = reserved+sent for the UTC day. Replay of message_key does not consume a second slot.';
