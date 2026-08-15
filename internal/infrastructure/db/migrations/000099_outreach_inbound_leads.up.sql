-- Durable web-cfg inbound receipt. Persisted before enrichment so a
-- lookup failure cannot drop the lead. Not a second CRM: commercial
-- action and outcome stay on the existing Warmbly tables.

CREATE TABLE IF NOT EXISTS outreach_inbound_leads (
    id                          uuid PRIMARY KEY,
    organization_id             uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    lead_id                     text NOT NULL,
    receipt_id                  text NOT NULL DEFAULT '',
    identity_key                text NOT NULL DEFAULT '',

    lead_created_at             timestamptz NOT NULL,
    warmbly_ingested_at         timestamptz NOT NULL,
    enrichment_completed_at     timestamptz,
    owner_assigned_at           timestamptz,
    first_action_at             timestamptz,
    conversation_at             timestamptz,
    proposal_at                 timestamptz,
    close_at                    timestamptz,

    source                      text NOT NULL DEFAULT '',
    route_family                text NOT NULL DEFAULT '',
    asset_id                    text NOT NULL DEFAULT '',
    cta_id                      text NOT NULL DEFAULT '',
    landing_url                 text NOT NULL DEFAULT '',
    contract_public_id          text NOT NULL DEFAULT '',
    entity_public_id            text NOT NULL DEFAULT '',
    cnpj14                      text NOT NULL DEFAULT '',
    company_name                text NOT NULL DEFAULT '',
    lead_name                   text NOT NULL DEFAULT '',
    lead_email                  text NOT NULL DEFAULT '',
    lead_phone                  text NOT NULL DEFAULT '',
    referrer                    text NOT NULL DEFAULT '',
    message                     text NOT NULL DEFAULT '',
    correlation_id              text NOT NULL DEFAULT '',

    consent_json                jsonb NOT NULL DEFAULT '{}'::jsonb,
    utm_json                    jsonb NOT NULL DEFAULT '{}'::jsonb,
    raw_payload                 jsonb NOT NULL DEFAULT '{}'::jsonb,

    enrichment_status           text NOT NULL DEFAULT 'UNKNOWN',
    next_action                 text NOT NULL DEFAULT '',
    channel                     text NOT NULL DEFAULT '',
    why_now                     text NOT NULL DEFAULT '',
    owner                       text NOT NULL DEFAULT 'UNKNOWN',
    status                      text NOT NULL DEFAULT 'OPEN',
    suppress_reason             text NOT NULL DEFAULT '',
    dedupe_of_lead_id           text NOT NULL DEFAULT '',
    person_id                   text NOT NULL DEFAULT '',
    person_name                 text NOT NULL DEFAULT '',
    evidence                    jsonb NOT NULL DEFAULT '[]'::jsonb,
    provenance                  jsonb NOT NULL DEFAULT '[]'::jsonb,
    warnings                    jsonb NOT NULL DEFAULT '[]'::jsonb,

    account_id                  uuid REFERENCES outreach_accounts(id) ON DELETE SET NULL,
    contact_candidate_id        uuid REFERENCES outreach_contact_candidates(id) ON DELETE SET NULL,
    commercial_action_id        uuid REFERENCES outreach_commercial_actions(id) ON DELETE SET NULL,

    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT outreach_inbound_leads_enrichment_check CHECK (enrichment_status IN (
        'UNKNOWN', 'COMPLETED', 'FAILED', 'UNAVAILABLE')),
    CONSTRAINT outreach_inbound_leads_status_check CHECK (status IN (
        'OPEN', 'SUPPRESSED', 'CLOSED'))
);

CREATE UNIQUE INDEX IF NOT EXISTS outreach_inbound_leads_org_lead_uidx
    ON outreach_inbound_leads (organization_id, lead_id);

CREATE INDEX IF NOT EXISTS outreach_inbound_leads_identity_idx
    ON outreach_inbound_leads (organization_id, identity_key, warmbly_ingested_at DESC)
    WHERE identity_key <> '';

CREATE INDEX IF NOT EXISTS outreach_inbound_leads_now_idx
    ON outreach_inbound_leads (organization_id, status, warmbly_ingested_at DESC);
