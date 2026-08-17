-- Additive commercial-intelligence snapshots for offer/capacity/finance
-- onboarding (issue #47 offer/revenue consumer). Own intel plane only.
-- Not a billing ledger. Provider IDs are optional references.

ALTER TABLE outreach_intel_exceptions
    DROP CONSTRAINT IF EXISTS outreach_intel_exceptions_code_check;

ALTER TABLE outreach_intel_exceptions
    ADD CONSTRAINT outreach_intel_exceptions_code_check CHECK (code IN (
        'orphan',
        'duplicate',
        'conflicting_account',
        'missing_version',
        'stale_attribution',
        'out_of_order',
        'unconfirmed_won',
        'unconfirmed_lost',
        'ledger_unavailable',
        'impossible_transition',
        'negative_latency',
        'overlapping_latency',
        'outbound_as_inbound',
        'invalid_asset_family',
        'missing_attribution',
        'terms_price_version_drift',
        'capacity_expired',
        'capacity_lost',
        'no_capacity',
        'duplicate_cnpj',
        'payment_overdue',
        'payment_refund',
        'payment_chargeback',
        'subscription_ended',
        'unknown_provider_event',
        'invalid_secret',
        'provider_unavailable',
        'onboarding_before_payment',
        'silent_renewal_refused',
        'private_extra_as_offer',
        'conflicting_external_reference',
        'created_object_as_revenue',
        'impossible_commercial_transition',
        'pii_rejected',
        'capacity_waitlisted',
        'capacity_rejected',
        'checkout_expired',
        'recurring_end_state'
    ));

CREATE TABLE IF NOT EXISTS outreach_intel_event_receipts (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    provider_event_id   text NOT NULL,
    external_reference  text NOT NULL DEFAULT '',
    event_id            text NOT NULL DEFAULT '',
    identity            text NOT NULL DEFAULT '',
    type                text NOT NULL DEFAULT '',
    raw_type            text NOT NULL DEFAULT '',
    raw_status          text NOT NULL DEFAULT '',
    acked               boolean NOT NULL DEFAULT true,
    processed           boolean NOT NULL DEFAULT false,
    synthetic           boolean NOT NULL DEFAULT false,
    payload             jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at          timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS outreach_intel_event_receipts_org_provider_uidx
    ON outreach_intel_event_receipts (organization_id, provider_event_id);

CREATE TABLE IF NOT EXISTS outreach_intel_capacity_holds (
    hold_id             text PRIMARY KEY,
    organization_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    lead_id             text NOT NULL DEFAULT '',
    units               integer NOT NULL DEFAULT 1,
    state               text NOT NULL DEFAULT 'held',
    created_at          timestamptz NOT NULL DEFAULT now(),
    expires_at          timestamptz NOT NULL,
    finalized_at        timestamptz,
    payload             jsonb NOT NULL DEFAULT '{}'::jsonb,
    CONSTRAINT outreach_intel_capacity_holds_state_check CHECK (state IN (
        'none', 'held', 'approved', 'rejected', 'waitlisted', 'expired', 'reserved', 'released'
    )),
    CONSTRAINT outreach_intel_capacity_holds_units_check CHECK (units > 0)
);

CREATE INDEX IF NOT EXISTS outreach_intel_capacity_holds_org_state_idx
    ON outreach_intel_capacity_holds (organization_id, state, expires_at);

COMMENT ON TABLE outreach_intel_event_receipts IS
'Durable provider/manual commercial event receipts. Dedupe by provider_event_id. Not revenue authority.';
COMMENT ON TABLE outreach_intel_capacity_holds IS
'Versioned capacity holds (policy capacity.policy.v1, limit 50, TTL 72h). Not a billing reservation engine.';
