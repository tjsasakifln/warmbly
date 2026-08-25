ALTER TABLE confenge_cohort_versions
    ADD COLUMN IF NOT EXISTS selection_mode text NOT NULL DEFAULT 'LEGACY',
    ADD COLUMN IF NOT EXISTS selection_report jsonb NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE confenge_cohort_versions
    DROP CONSTRAINT IF EXISTS confenge_cohort_versions_selection_mode_check;

ALTER TABLE confenge_cohort_versions
    ADD CONSTRAINT confenge_cohort_versions_selection_mode_check
    CHECK (selection_mode IN ('LEGACY', 'NEXT_UNCLAIMED', 'RECOVER_PRIOR'));

CREATE TABLE IF NOT EXISTS confenge_cohort_selection_claims (
    id                  uuid PRIMARY KEY,
    organization_id     uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    source_run_id       text NOT NULL,
    cohort_version_id   uuid NOT NULL REFERENCES confenge_cohort_versions(id) ON DELETE RESTRICT,
    account_id          uuid NOT NULL REFERENCES outreach_accounts(id) ON DELETE RESTRICT,
    cnpj_root           text NOT NULL CHECK (cnpj_root ~ '^[0-9]{8}$'),
    recipient_hash      text NOT NULL CHECK (recipient_hash ~ '^[a-f0-9]{64}$'),
    selection_mode      text NOT NULL CHECK (selection_mode IN ('NEXT_UNCLAIMED', 'RECOVER_PRIOR')),
    created_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT confenge_cohort_claim_account_unique
        UNIQUE (organization_id, source_run_id, account_id),
    CONSTRAINT confenge_cohort_claim_supplier_unique
        UNIQUE (organization_id, source_run_id, cnpj_root),
    CONSTRAINT confenge_cohort_claim_recipient_unique
        UNIQUE (organization_id, source_run_id, recipient_hash)
);

CREATE INDEX IF NOT EXISTS confenge_cohort_selection_claims_version_idx
    ON confenge_cohort_selection_claims (cohort_version_id);

CREATE INDEX IF NOT EXISTS confenge_cohort_selection_claims_progress_idx
    ON confenge_cohort_selection_claims (organization_id, source_run_id, created_at, id);
