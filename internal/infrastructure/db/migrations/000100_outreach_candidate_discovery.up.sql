-- extra-cli #392 discovery dimensions (derivation, epistemic class, freshness,
-- suppression, passive email verification). Visibility/provenance only.
ALTER TABLE outreach_contact_candidates
    ADD COLUMN IF NOT EXISTS email_derivation text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS channel_epistemic_class text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS route_freshness text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS route_suppression text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS discovery_json jsonb;

COMMENT ON COLUMN outreach_contact_candidates.email_derivation IS
'OBSERVED vs INFERRED. Never inferred from MX/DNS. extra-cli is the source.';
COMMENT ON COLUMN outreach_contact_candidates.discovery_json IS
'Passive DNS/MX/catch-all/SMTP plus identity_proven. MX never authorizes send.';
