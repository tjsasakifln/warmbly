-- Additive organic-attribution exception codes for the inbound learning
-- loop (issue #47 residual). Own intel plane only. Not a CRM, GSC store,
-- or second ledger. Individual search queries stay off lead rows.

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
        'alert_store_failed',
        'lead_without_asset_id',
        'unknown_asset_version',
        'contradictory_source',
        'synthetic_treated_as_real',
        'missing_consent',
        'pipeline_without_evidence',
        'revenue_without_financial_event',
        'gsc_query_on_lead',
        'query_hash_on_lead'
    ));

COMMENT ON CONSTRAINT outreach_intel_exceptions_code_check ON outreach_intel_exceptions IS
'Versioned intel exception codes including organic attribution conflicts. Individual GSC queries never join a person.';

-- Durable aggregated search observations. Not a lead, not a GSC warehouse,
-- and never stores individual query or query_hash.
CREATE TABLE IF NOT EXISTS outreach_intel_search_observations (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    event_id            text NOT NULL,
    receipt_id          text NOT NULL DEFAULT '',
    payload_hash        text NOT NULL,
    "window"            text NOT NULL,
    organic_source      text NOT NULL DEFAULT 'UNKNOWN',
    asset_family        text NOT NULL DEFAULT '',
    asset_id            text NOT NULL DEFAULT '',
    landing_path        text NOT NULL DEFAULT '',
    intent_class        text NOT NULL DEFAULT '',
    query_class         text NOT NULL DEFAULT '',
    eligible            integer,
    appeared            integer,
    clicked             integer,
    engaged             integer,
    coverage            text NOT NULL DEFAULT 'UNKNOWN',
    freshness           text NOT NULL DEFAULT '',
    producer_source     text NOT NULL DEFAULT '',
    producer_sha        text NOT NULL DEFAULT '',
    synthetic           boolean NOT NULL DEFAULT false,
    record_kind         text NOT NULL DEFAULT '',
    consent_policy      text NOT NULL DEFAULT 'not_applicable',
    measurement_at      timestamptz NOT NULL,
    replay              boolean NOT NULL DEFAULT false,
    out_of_order        boolean NOT NULL DEFAULT false,
    payload             jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT outreach_intel_search_obs_window_check CHECK ("window" IN (
        '7d_complete', '28d_complete', '90d', 'open_censored')),
    CONSTRAINT outreach_intel_search_obs_source_check CHECK (organic_source IN (
        'organic_search', 'direct', 'referral', 'ai_referral', 'partner', 'outbound', 'UNKNOWN')),
    CONSTRAINT outreach_intel_search_obs_counts_check CHECK (
        (eligible IS NULL OR eligible >= 0) AND
        (appeared IS NULL OR appeared >= 0) AND
        (clicked IS NULL OR clicked >= 0) AND
        (engaged IS NULL OR engaged >= 0)
    ),
    CONSTRAINT outreach_intel_search_obs_coverage_check CHECK (coverage IN (
        'OBSERVED', 'ABSENT', 'BLOCKED', 'UNKNOWN')),
    CONSTRAINT outreach_intel_search_obs_consent_check CHECK (consent_policy IN (
        'not_applicable', 'aggregate')),
    CONSTRAINT outreach_intel_search_obs_no_query CHECK (
        NOT (payload ? 'query') AND NOT (payload ? 'query_hash') AND NOT (payload ? 'gsc_query') AND NOT (payload ? 'GSCQuery')
    )
);

CREATE UNIQUE INDEX IF NOT EXISTS outreach_intel_search_obs_org_event_uidx
    ON outreach_intel_search_observations (organization_id, event_id);

CREATE INDEX IF NOT EXISTS outreach_intel_search_obs_org_window_idx
    ON outreach_intel_search_observations (organization_id, "window", measurement_at DESC);

COMMENT ON TABLE outreach_intel_search_observations IS
'Aggregated search observations (confenge.search_observation.v1). Counts nullable. No individual query/query_hash. Not a lead.';
