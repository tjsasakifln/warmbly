-- Reverse 000103. Restores the 000102 exception CHECK. Drops additive
-- receipt and capacity tables. Chain jsonb snapshots remain readable
-- by older code (unknown fields ignored).

DROP TABLE IF EXISTS outreach_intel_capacity_holds;
DROP TABLE IF EXISTS outreach_intel_event_receipts;

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
        'missing_attribution'
    ));
