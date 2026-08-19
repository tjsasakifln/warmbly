-- Durable operator-attention alerts for inbound leads. Additive: one
-- logical alert per org/lead/alert_type. Replay and restart do not
-- insert a second row. This is not a CRM and not a send path.

CREATE TABLE IF NOT EXISTS outreach_operator_alerts (
    id                  uuid PRIMARY KEY,
    organization_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    lead_id             text NOT NULL,
    receipt_id          text NOT NULL DEFAULT '',
    event_id            text NOT NULL,
    alert_type          text NOT NULL DEFAULT 'inbound_operator_attention',
    synthetic           boolean NOT NULL DEFAULT false,

    created_at          timestamptz NOT NULL DEFAULT now(),
    first_emitted_at    timestamptz,
    last_emitted_at     timestamptz,

    channel_states      jsonb NOT NULL DEFAULT '{}'::jsonb,

    acknowledged_at     timestamptz,
    acknowledged_by     text NOT NULL DEFAULT '',
    first_action_at     timestamptz,
    first_action_type   text NOT NULL DEFAULT '',
    resolved_at         timestamptz,
    resolution_reason   text NOT NULL DEFAULT '',

    attempt_count       integer NOT NULL DEFAULT 0,
    next_attempt_at     timestamptz,
    failure_code        text NOT NULL DEFAULT '',
    owner               text NOT NULL DEFAULT 'UNKNOWN',
    freshness           text NOT NULL DEFAULT '',
    state               text NOT NULL DEFAULT 'NEW',
    policy_version      text NOT NULL DEFAULT 'v1',
    updated_at          timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT outreach_operator_alerts_type_check CHECK (alert_type = 'inbound_operator_attention'),
    CONSTRAINT outreach_operator_alerts_state_check CHECK (state IN (
        'NEW', 'ATTENTION', 'AGED', 'ACKNOWLEDGED', 'ACTION_RECORDED',
        'RESOLVED_NO_ACTION', 'ALERT_FAILED'
    ))
);

CREATE UNIQUE INDEX IF NOT EXISTS outreach_operator_alerts_org_event_uidx
    ON outreach_operator_alerts (organization_id, event_id);

CREATE UNIQUE INDEX IF NOT EXISTS outreach_operator_alerts_org_lead_type_uidx
    ON outreach_operator_alerts (organization_id, lead_id, alert_type);

CREATE INDEX IF NOT EXISTS outreach_operator_alerts_now_idx
    ON outreach_operator_alerts (organization_id, synthetic, state, created_at DESC);

-- Alert-store failure is a held exception; receipt stays on outreach_inbound_leads.
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
        'recurring_end_state',
        'counsel_review_due',
        'nfse_manual_queue',
        'alert_store_failed'
    ));
