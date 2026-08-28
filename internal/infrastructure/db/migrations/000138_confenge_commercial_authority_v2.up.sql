-- COMMERCIAL_AUTHORITY/2.0: a CONFENGE lead is qualified by public evidence of
-- being the contracted supplier on an engineering work/service inside a rolling
-- three-year window. Source/crawler age is acquisition health and never revokes
-- this. These columns make the qualifying fact durable and auditable.

ALTER TABLE outreach_accounts
    ADD COLUMN IF NOT EXISTS commercial_qualification_state TEXT NOT NULL DEFAULT 'UNKNOWN',
    ADD COLUMN IF NOT EXISTS commercial_qualification_policy_version TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS commercial_qualifying_contract_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS commercial_qualifying_contract_date DATE,
    ADD COLUMN IF NOT EXISTS commercial_qualifying_date_field TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS commercial_qualifying_contract_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS commercial_qualified_until DATE,
    ADD COLUMN IF NOT EXISTS commercial_qualification_evidence_hash TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS commercial_qualification_evidence_reference TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS commercial_qualification_provenance TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS commercial_qualification_cnpj_root8 TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS commercial_qualification_observed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS commercial_qualification_deactivated BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS commercial_qualification_deactivation_reason TEXT NOT NULL DEFAULT '';

ALTER TABLE outreach_accounts
    DROP CONSTRAINT IF EXISTS outreach_accounts_commercial_qualification_state_check;
ALTER TABLE outreach_accounts
    ADD CONSTRAINT outreach_accounts_commercial_qualification_state_check
    CHECK (commercial_qualification_state IN ('QUALIFIED', 'EXPIRED', 'REVOKED', 'UNKNOWN'));

-- The transport gate reads this predicate per account on the send path.
CREATE INDEX IF NOT EXISTS idx_outreach_accounts_commercial_qualified
    ON outreach_accounts (organization_id, commercial_qualification_state, commercial_qualified_until)
    WHERE commercial_qualification_state = 'QUALIFIED';

CREATE INDEX IF NOT EXISTS idx_outreach_accounts_commercial_root8
    ON outreach_accounts (organization_id, commercial_qualification_cnpj_root8)
    WHERE commercial_qualification_cnpj_root8 <> '';

-- Population-level attestation for the applied feed.
ALTER TABLE outreach_feed_sync_state
    ADD COLUMN IF NOT EXISTS commercial_authority_v2 JSONB,
    ADD COLUMN IF NOT EXISTS qualification_evidence_hash TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS qualified_root_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS qualification_window_years INTEGER NOT NULL DEFAULT 0;

COMMENT ON COLUMN outreach_accounts.commercial_qualified_until IS
    'Qualifying contracting date + 3 years. Natural expiry of the fact itself; never a crawler TTL.';
COMMENT ON COLUMN outreach_feed_sync_state.qualification_evidence_hash IS
    'sha256 over the sorted per-root qualification digests of the applied publication.';
