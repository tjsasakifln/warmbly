-- 000128: 000127 is org risk/list safety on current main.
ALTER TABLE outreach_accounts
    ADD COLUMN IF NOT EXISTS contractor_role_status text NOT NULL DEFAULT 'UNKNOWN',
    ADD COLUMN IF NOT EXISTS target_party_role text NOT NULL DEFAULT 'UNKNOWN',
    ADD COLUMN IF NOT EXISTS contractor_role_policy_version text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS contractor_role_source text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS contractor_role_source_run_id text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS contractor_role_observed_at timestamptz,
    ADD COLUMN IF NOT EXISTS contractor_role_evidence_hash text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS contractor_role_evidence_reference text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS contractor_role_evidence_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS supplier_cnpj14 text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS supplier_identity_ref text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS buyer_cnpj14 text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS buyer_identity_ref text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS contractor_role_match_method text NOT NULL DEFAULT 'NONE',
    ADD COLUMN IF NOT EXISTS contractor_role_confidence text NOT NULL DEFAULT 'UNKNOWN',
    ADD COLUMN IF NOT EXISTS contractor_role_reason_codes jsonb NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE outreach_accounts
    DROP CONSTRAINT IF EXISTS outreach_accounts_contractor_role_status_check,
    ADD CONSTRAINT outreach_accounts_contractor_role_status_check
        CHECK (contractor_role_status IN ('CONTRACTOR_ROLE_CONFIRMED','PARTY_ROLE_CONFLICT','UNKNOWN')),
    DROP CONSTRAINT IF EXISTS outreach_accounts_target_party_role_check,
    ADD CONSTRAINT outreach_accounts_target_party_role_check
        CHECK (target_party_role IN ('SUPPLIER','BUYER_CONFLICT','UNKNOWN'));

CREATE INDEX IF NOT EXISTS outreach_accounts_contractor_role_gate_idx
    ON outreach_accounts (organization_id, source_run_id, contractor_role_status, target_party_role);

CREATE TABLE IF NOT EXISTS confenge_delegated_first_touch_batches (
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    batch_id text NOT NULL,
    agent_id text NOT NULL,
    policy_version text NOT NULL,
    policy_authorization_id uuid NOT NULL REFERENCES confenge_campaign_policy_authorizations(id),
    source_run_id text NOT NULL,
    evidence_version text NOT NULL,
    policy_hash text NOT NULL,
    authority_reference text NOT NULL,
    source_snapshot_hash text NOT NULL,
    composer_version text NOT NULL,
    template_version text NOT NULL,
    prompt_version text NOT NULL,
    runtime_release_sha text NOT NULL,
    manifest_hash text NOT NULL,
    status text NOT NULL CHECK (status IN ('RESERVED','DRY_RUN','APPLIED','PARTIAL','FAILED')),
    counts jsonb NOT NULL DEFAULT '{}'::jsonb,
    generated_at timestamptz NOT NULL,
    reserved_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, batch_id),
    CONSTRAINT confenge_delegated_batch_policy_v1 CHECK (policy_version = 'CFG-FIRST-TOUCH-ROUTING-v1'),
    CONSTRAINT confenge_delegated_batch_manifest_hash CHECK (manifest_hash ~ '^[0-9a-f]{64}$')
);

CREATE TABLE IF NOT EXISTS confenge_delegated_first_touch_decisions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    batch_id text NOT NULL,
    account_id uuid REFERENCES outreach_accounts(id) ON DELETE SET NULL,
    contact_candidate_id uuid REFERENCES outreach_contact_candidates(id) ON DELETE SET NULL,
    touchpoint_id uuid REFERENCES outreach_touchpoints(id) ON DELETE SET NULL,
    draft_id uuid REFERENCES outreach_drafts(id) ON DELETE SET NULL,
    policy_authorization_id uuid NOT NULL REFERENCES confenge_campaign_policy_authorizations(id),
    policy_version text NOT NULL,
    agent_id text NOT NULL,
    authority text NOT NULL,
    approved_by_type text NOT NULL,
    decision text NOT NULL,
    state text NOT NULL CHECK (state IN ('HOLD','APPROVED','QUEUED','SENT','APPROVED_NOT_SCHEDULED','CANCELLED')),
    cnpj14 text NOT NULL,
    cnpj_root text NOT NULL,
    supplier_cnpj14 text NOT NULL,
    buyer_cnpj14 text NOT NULL DEFAULT '',
    contractor_role_status text NOT NULL,
    contract_role_source text NOT NULL,
    contract_evidence_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
    reconciliation_status text NOT NULL,
    route_class text NOT NULL,
    evidence_version text NOT NULL,
    evidence_source_run_id text NOT NULL,
    source_snapshot_hash text NOT NULL,
    evidence_hash text NOT NULL,
    evidence_reference text NOT NULL DEFAULT '',
    evidence_observed_at timestamptz NOT NULL,
    web_sources jsonb NOT NULL DEFAULT '[]'::jsonb,
    subject_hash text NOT NULL,
    body_hash text NOT NULL,
    content_hash text NOT NULL DEFAULT '',
    recipient text NOT NULL DEFAULT '',
    target_party_role text NOT NULL DEFAULT 'UNKNOWN',
    supplier_identity_ref text NOT NULL DEFAULT '',
    buyer_identity_ref text NOT NULL DEFAULT '',
    role_match_method text NOT NULL DEFAULT 'NONE',
    role_confidence text NOT NULL DEFAULT 'UNKNOWN',
    role_reason_codes jsonb NOT NULL DEFAULT '[]'::jsonb,
    policy_hash text NOT NULL,
    authority_reference text NOT NULL,
    composer_version text NOT NULL,
    template_version text NOT NULL,
    prompt_version text NOT NULL,
    runtime_release_sha text NOT NULL,
    material_binding_hash text NOT NULL,
    qa_result text NOT NULL,
    qa_attempts integer NOT NULL DEFAULT 0,
    qa_repaired boolean NOT NULL DEFAULT false,
    reason_codes jsonb NOT NULL DEFAULT '[]'::jsonb,
    blocker_codes jsonb NOT NULL DEFAULT '[]'::jsonb,
    correlation_id text NOT NULL,
    idempotency_key text NOT NULL,
    queue_message_key text NOT NULL DEFAULT '',
    due_at timestamptz,
    readback_at timestamptz,
    sent_at timestamptz,
    decided_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (organization_id, batch_id)
        REFERENCES confenge_delegated_first_touch_batches(organization_id, batch_id)
        ON DELETE CASCADE,
    CONSTRAINT confenge_delegated_decision_policy_v1 CHECK (policy_version = 'CFG-FIRST-TOUCH-ROUTING-v1'),
    CONSTRAINT confenge_delegated_decision_authority CHECK (authority = 'founder-approved-first-touch-policy'),
    CONSTRAINT confenge_delegated_decision_actor CHECK (approved_by_type = 'delegated_agent'),
    CONSTRAINT confenge_delegated_decision_kind CHECK (decision IN ('DELEGATED_POLICY_APPROVE','HOLD')),
    CONSTRAINT confenge_delegated_decision_role CHECK (contractor_role_status IN ('CONTRACTOR_ROLE_CONFIRMED','PARTY_ROLE_CONFLICT','UNKNOWN')),
    CONSTRAINT confenge_delegated_decision_target_role CHECK (target_party_role IN ('SUPPLIER','BUYER_CONFLICT','UNKNOWN')),
    CONSTRAINT confenge_delegated_decision_role_source CHECK (decision = 'HOLD' OR length(contract_role_source) > 0),
    CONSTRAINT confenge_delegated_decision_contract_evidence CHECK (decision = 'HOLD' OR (jsonb_typeof(contract_evidence_ids) = 'array' AND jsonb_array_length(contract_evidence_ids) > 0)),
    CONSTRAINT confenge_delegated_decision_reconciliation CHECK (reconciliation_status IN ('DATALAKE+WEB_CORROBORATED','DATALAKE_IDENTITY + WEB_CONTACT','CONFLICT','UNKNOWN')),
    CONSTRAINT confenge_delegated_decision_route CHECK (route_class IN ('DIRECT_PERSON','ROLE_OR_DEPARTMENT','GENERIC_COMPANY','PUBLIC_COMPANY_FREEMAIL','UNKNOWN')),
    CONSTRAINT confenge_delegated_decision_cnpj14 CHECK (cnpj14 ~ '^[0-9]{14}$'),
    CONSTRAINT confenge_delegated_decision_cnpj_root CHECK (cnpj_root ~ '^[0-9]{8}$'),
    CONSTRAINT confenge_delegated_decision_supplier CHECK (decision = 'HOLD' OR supplier_cnpj14 ~ '^[0-9]{14}$'),
    CONSTRAINT confenge_delegated_decision_policy_hash CHECK (policy_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT confenge_delegated_decision_material_hash CHECK (material_binding_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT confenge_delegated_decision_evidence_hash CHECK (decision = 'HOLD' OR evidence_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT confenge_delegated_decision_subject_hash CHECK (decision = 'HOLD' OR subject_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT confenge_delegated_decision_body_hash CHECK (decision = 'HOLD' OR body_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT confenge_delegated_decision_qa CHECK (qa_result IN ('PASS','FAIL','NOT_RUN')),
    CONSTRAINT confenge_delegated_decision_attempts CHECK (qa_attempts BETWEEN 0 AND 3),
    UNIQUE (organization_id, idempotency_key)
);

CREATE UNIQUE INDEX IF NOT EXISTS confenge_delegated_first_touch_one_live_account_idx
    ON confenge_delegated_first_touch_decisions (organization_id, account_id, policy_version)
    WHERE account_id IS NOT NULL AND state IN ('APPROVED','QUEUED','SENT','APPROVED_NOT_SCHEDULED');

CREATE UNIQUE INDEX IF NOT EXISTS confenge_delegated_first_touch_one_live_root_idx
    ON confenge_delegated_first_touch_decisions (organization_id, cnpj_root, policy_version)
    WHERE state IN ('APPROVED','QUEUED','SENT','APPROVED_NOT_SCHEDULED');

CREATE INDEX IF NOT EXISTS confenge_delegated_first_touch_batch_state_idx
    ON confenge_delegated_first_touch_decisions (organization_id, batch_id, state);

COMMENT ON TABLE confenge_delegated_first_touch_batches IS
'Agent-authored first-touch manifests. This is an audit/resume plane, never a transport queue.';

COMMENT ON TABLE confenge_delegated_first_touch_decisions IS
'Per-message delegated decisions and canonical queue readbacks. approved_by remains null on the touchpoint.';
