-- Governance#65 inbound-only commercial representations.
--
-- A net-new REQUEST_* contact is admitted as an outreach account that can
-- never become outbound-eligible by default. The generated column is the
-- SQL-level predicate; source_system remains the durable write marker.

ALTER TABLE outreach_accounts
    ADD COLUMN IF NOT EXISTS inbound_only boolean
    GENERATED ALWAYS AS (lower(btrim(source_system)) = 'inbound_only') STORED;

CREATE UNIQUE INDEX IF NOT EXISTS outreach_accounts_inbound_only_identity_uidx
    ON outreach_accounts (organization_id, source_lead_id)
    WHERE inbound_only AND source_lead_id <> '';

ALTER TABLE outreach_accounts
    DROP CONSTRAINT IF EXISTS outreach_accounts_inbound_only_not_outbound;
ALTER TABLE outreach_accounts
    ADD CONSTRAINT outreach_accounts_inbound_only_not_outbound
    CHECK (
        NOT inbound_only
        OR (
            NOT COALESCE(target_fit_eligible, false)
            AND NOT COALESCE(email_send_ready, false)
        )
    );

COMMENT ON COLUMN outreach_accounts.inbound_only IS
    'True when source_system=inbound_only. These rows receive hand-raisers and never gain outbound eligibility by default.';
