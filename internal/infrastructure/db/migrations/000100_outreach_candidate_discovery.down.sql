ALTER TABLE outreach_contact_candidates
    DROP COLUMN IF EXISTS discovery_json,
    DROP COLUMN IF EXISTS route_suppression,
    DROP COLUMN IF EXISTS route_freshness,
    DROP COLUMN IF EXISTS channel_epistemic_class,
    DROP COLUMN IF EXISTS email_derivation;
