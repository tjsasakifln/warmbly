-- Commercial intelligence (issue #47). Own tables, not a CRM and not a
-- second execution ledger. Joins copy IDs from web-cfg / extra-cli / Warmbly.

CREATE TABLE IF NOT EXISTS outreach_intel_chains (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    identity            text NOT NULL,
    metric_key          text NOT NULL,
    route_family        text NOT NULL DEFAULT 'UNKNOWN',
    label               text NOT NULL DEFAULT 'REAL',
    synthetic           boolean NOT NULL DEFAULT false,
    payload             jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT outreach_intel_chains_family_check CHECK (route_family IN (
        'inbound', 'outbound', 'partner', 'expansion', 'UNKNOWN')),
    CONSTRAINT outreach_intel_chains_label_check CHECK (label IN ('REAL', 'SYNTHETIC'))
);

CREATE UNIQUE INDEX IF NOT EXISTS outreach_intel_chains_org_identity_uidx
    ON outreach_intel_chains (organization_id, identity);

CREATE INDEX IF NOT EXISTS outreach_intel_chains_org_month_idx
    ON outreach_intel_chains (organization_id, created_at DESC);

CREATE TABLE IF NOT EXISTS outreach_intel_exceptions (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    code                text NOT NULL,
    identity            text NOT NULL DEFAULT '',
    metric_key          text NOT NULL DEFAULT '',
    held                boolean NOT NULL DEFAULT false,
    payload             jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT outreach_intel_exceptions_code_check CHECK (code IN (
        'orphan',
        'duplicate',
        'conflicting_account',
        'missing_version',
        'stale_attribution',
        'out_of_order',
        'unconfirmed_won',
        'ledger_unavailable'
    ))
);

CREATE INDEX IF NOT EXISTS outreach_intel_exceptions_org_code_idx
    ON outreach_intel_exceptions (organization_id, code, created_at DESC);

CREATE TABLE IF NOT EXISTS outreach_intel_learning_candidates (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    target              text NOT NULL DEFAULT 'UNKNOWN',
    source              text NOT NULL DEFAULT 'UNKNOWN',
    status              text NOT NULL DEFAULT 'PENDING',
    identity            text NOT NULL DEFAULT '',
    payload             jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT outreach_intel_learning_target_check CHECK (target IN (
        'demand', 'asset', 'offer', 'content', 'distribution', 'UNKNOWN')),
    CONSTRAINT outreach_intel_learning_source_check CHECK (source IN (
        'correction', 'outcome', 'UNKNOWN')),
    CONSTRAINT outreach_intel_learning_status_check CHECK (status IN ('PENDING'))
);

CREATE INDEX IF NOT EXISTS outreach_intel_learning_org_idx
    ON outreach_intel_learning_candidates (organization_id, created_at DESC);

COMMENT ON TABLE outreach_intel_chains IS
'Observed commercial join. Metric keys are ID hashes, never PII. Not causal proof.';
COMMENT ON TABLE outreach_intel_exceptions IS
'Orphan/duplicate/conflict/missing_version/stale/out_of_order/unconfirmed_won queue.';
COMMENT ON TABLE outreach_intel_learning_candidates IS
'Local LEARNING CANDIDATE rows. Never written to extra-cli, web-cfg, or SmartLic.';
