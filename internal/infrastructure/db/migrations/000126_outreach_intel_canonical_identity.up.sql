-- Opaque cross-system identity aliases for one commercial correlation.
-- Display names and PII are forbidden as keys. Warmbly remains outcome owner;
-- this table is not a CRM or a financial ledger.
CREATE TABLE IF NOT EXISTS outreach_intel_identity_links (
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    correlation_id  text NOT NULL,
    entity_kind     text NOT NULL CHECK (entity_kind IN (
        'account', 'opportunity', 'offer', 'proposal', 'charge', 'payment'
    )),
    entity_id       text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, correlation_id, entity_kind, entity_id),
    CONSTRAINT outreach_intel_identity_correlation_safe CHECK (
        length(correlation_id) BETWEEN 1 AND 160
        AND correlation_id !~ '[[:space:]@]'
    ),
    CONSTRAINT outreach_intel_identity_entity_safe CHECK (
        length(entity_id) BETWEEN 1 AND 160
        AND entity_id !~ '[[:space:]@]'
    )
);

CREATE INDEX IF NOT EXISTS outreach_intel_identity_links_correlation_idx
    ON outreach_intel_identity_links (organization_id, correlation_id);

CREATE UNIQUE INDEX IF NOT EXISTS outreach_intel_identity_links_singleton_kind_idx
    ON outreach_intel_identity_links (organization_id, correlation_id, entity_kind)
    WHERE entity_kind IN ('account', 'opportunity', 'offer', 'proposal');

CREATE UNIQUE INDEX IF NOT EXISTS outreach_intel_identity_links_unique_entity_idx
    ON outreach_intel_identity_links (organization_id, entity_kind, entity_id)
    WHERE entity_kind IN ('opportunity', 'proposal', 'charge', 'payment');

UPDATE outreach_intel_event_receipts
SET payload = jsonb_set(payload, '{processed}', to_jsonb(processed), true);

WITH canonical AS (
    SELECT
        organization_id,
        COALESCE(
            NULLIF(NULLIF(payload ->> 'correlation_id', ''), 'UNKNOWN'),
            NULLIF(NULLIF(payload #>> '{keys,correlation_id}', ''), 'UNKNOWN'),
            NULLIF(NULLIF(payload #>> '{keys,external_reference}', ''), 'UNKNOWN'),
            NULLIF(NULLIF(payload #>> '{commercial,provider,external_reference}', ''), 'UNKNOWN')
        ) AS correlation_id,
        payload
    FROM outreach_intel_chains
), links AS (
    SELECT
        organization_id,
        correlation_id,
        link.entity_kind,
        link.entity_id
    FROM canonical
    CROSS JOIN LATERAL (VALUES
        ('account', COALESCE(
            NULLIF(NULLIF(payload ->> 'account_id', ''), 'UNKNOWN'),
            NULLIF(NULLIF(payload #>> '{keys,account_id}', ''), 'UNKNOWN')
        )),
        ('opportunity', COALESCE(
            NULLIF(NULLIF(payload ->> 'opportunity_id', ''), 'UNKNOWN'),
            NULLIF(NULLIF(payload #>> '{keys,opportunity_id}', ''), 'UNKNOWN')
        )),
        ('offer', COALESCE(
            NULLIF(NULLIF(payload ->> 'offer_id', ''), 'UNKNOWN'),
            NULLIF(NULLIF(payload #>> '{keys,offer_id}', ''), 'UNKNOWN'),
            NULLIF(NULLIF(payload #>> '{commercial,offer,offer_id}', ''), 'UNKNOWN')
        )),
        ('proposal', COALESCE(
            NULLIF(NULLIF(payload ->> 'proposal_id', ''), 'UNKNOWN'),
            NULLIF(NULLIF(payload #>> '{keys,proposal_id}', ''), 'UNKNOWN')
        )),
        ('charge', COALESCE(
            NULLIF(NULLIF(payload ->> 'charge_id', ''), 'UNKNOWN'),
            NULLIF(NULLIF(payload #>> '{keys,charge_id}', ''), 'UNKNOWN'),
            NULLIF(NULLIF(payload #>> '{commercial,provider,charge_id}', ''), 'UNKNOWN'),
            NULLIF(NULLIF(payload #>> '{commercial,provider,provider_payment_id}', ''), 'UNKNOWN')
        )),
        ('payment', COALESCE(
            NULLIF(NULLIF(payload ->> 'payment_id', ''), 'UNKNOWN'),
            NULLIF(NULLIF(payload #>> '{keys,payment_id}', ''), 'UNKNOWN'),
            CASE
                WHEN COALESCE((payload #>> '{commercial,payment,received_count}')::integer, 0) > 0
                THEN NULLIF(NULLIF(payload #>> '{commercial,provider,provider_payment_id}', ''), 'UNKNOWN')
            END
        ))
    ) AS link(entity_kind, entity_id)
)
INSERT INTO outreach_intel_identity_links (
    organization_id, correlation_id, entity_kind, entity_id
)
SELECT organization_id, correlation_id, entity_kind, entity_id
FROM links
WHERE correlation_id IS NOT NULL AND entity_id IS NOT NULL
ON CONFLICT (organization_id, correlation_id, entity_kind, entity_id) DO NOTHING;

COMMENT ON TABLE outreach_intel_identity_links IS
'Canonical opaque account/opportunity/offer/proposal/charge/payment aliases. Never keyed by a free-form name.';
