-- Reverse 000104. Restores the 000103 exception CHECK.

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
