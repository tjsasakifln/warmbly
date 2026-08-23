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
    PRIMARY KEY (organization_id, entity_kind, entity_id),
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

COMMENT ON TABLE outreach_intel_identity_links IS
'Canonical opaque account/opportunity/offer/proposal/charge/payment aliases. Never keyed by a free-form name.';
