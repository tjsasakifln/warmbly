ALTER TABLE outreach_accounts
    DROP CONSTRAINT IF EXISTS outreach_accounts_inbound_only_not_outbound;

DROP INDEX IF EXISTS outreach_accounts_inbound_only_identity_uidx;

ALTER TABLE outreach_accounts
    DROP COLUMN IF EXISTS inbound_only;
