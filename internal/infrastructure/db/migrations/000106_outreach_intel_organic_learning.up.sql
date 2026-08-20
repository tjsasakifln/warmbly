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
