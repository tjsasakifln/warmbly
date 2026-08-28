DROP INDEX IF EXISTS idx_outreach_accounts_commercial_root8;
DROP INDEX IF EXISTS idx_outreach_accounts_commercial_qualified;

ALTER TABLE outreach_accounts
    DROP CONSTRAINT IF EXISTS outreach_accounts_commercial_qualification_state_check;

ALTER TABLE outreach_accounts
    DROP COLUMN IF EXISTS commercial_qualification_state,
    DROP COLUMN IF EXISTS commercial_qualification_policy_version,
    DROP COLUMN IF EXISTS commercial_qualifying_contract_id,
    DROP COLUMN IF EXISTS commercial_qualifying_contract_date,
    DROP COLUMN IF EXISTS commercial_qualifying_date_field,
    DROP COLUMN IF EXISTS commercial_qualifying_contract_count,
    DROP COLUMN IF EXISTS commercial_qualified_until,
    DROP COLUMN IF EXISTS commercial_qualification_evidence_hash,
    DROP COLUMN IF EXISTS commercial_qualification_evidence_reference,
    DROP COLUMN IF EXISTS commercial_qualification_provenance,
    DROP COLUMN IF EXISTS commercial_qualification_cnpj_root8,
    DROP COLUMN IF EXISTS commercial_qualification_observed_at,
    DROP COLUMN IF EXISTS commercial_qualification_deactivated,
    DROP COLUMN IF EXISTS commercial_qualification_deactivation_reason;

ALTER TABLE outreach_feed_sync_state
    DROP COLUMN IF EXISTS commercial_authority_v2,
    DROP COLUMN IF EXISTS qualification_evidence_hash,
    DROP COLUMN IF EXISTS qualified_root_count,
    DROP COLUMN IF EXISTS qualification_window_years;
