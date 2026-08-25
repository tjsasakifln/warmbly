CREATE TABLE IF NOT EXISTS confenge_cohort_versions (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    cohort_id uuid NOT NULL,
    version integer NOT NULL CHECK (version > 0),
    contract_version text NOT NULL DEFAULT 'confenge.human-gate.v1',
    source_run_id text NOT NULL,
    source_system text NOT NULL,
    source_as_of timestamptz NOT NULL,
    freshness_expires_at timestamptz NOT NULL,
    policy_version text NOT NULL,
    frozen_hash text NOT NULL,
    frozen_manifest jsonb NOT NULL,
    reproduced_from_version integer,
    created_by uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    correlation_id text NOT NULL,
    idempotency_key text NOT NULL,
    request_hash text NOT NULL,
    CONSTRAINT confenge_cohort_versions_identity UNIQUE (organization_id, cohort_id, version),
    CONSTRAINT confenge_cohort_versions_idempotency UNIQUE (organization_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS confenge_cohort_versions_org_created_idx
    ON confenge_cohort_versions (organization_id, created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS confenge_cohort_validations (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    cohort_version_id uuid NOT NULL REFERENCES confenge_cohort_versions(id) ON DELETE RESTRICT,
    candidate_id uuid NOT NULL,
    status text NOT NULL CHECK (status IN ('VALID','RISKY','INVALID','UNKNOWN')),
    reason text NOT NULL,
    provider text NOT NULL,
    method text NOT NULL,
    evidence_hash text NOT NULL,
    checked_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    actor_id uuid NOT NULL,
    correlation_id text NOT NULL,
    receipt text NOT NULL,
    idempotency_key text NOT NULL,
    request_hash text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT confenge_cohort_validations_idempotency UNIQUE (organization_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS confenge_cohort_validations_latest_idx
    ON confenge_cohort_validations (cohort_version_id, candidate_id, created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS confenge_cohort_candidate_reviews (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    cohort_version_id uuid NOT NULL REFERENCES confenge_cohort_versions(id) ON DELETE RESTRICT,
    candidate_id uuid NOT NULL,
    decision text NOT NULL CHECK (decision IN ('APPROVE','REJECT','HOLD')),
    reason text NOT NULL,
    recipient_hash text NOT NULL,
    content_hash text NOT NULL,
    policy_version text NOT NULL,
    evidence_hash text NOT NULL,
    validation_id uuid REFERENCES confenge_cohort_validations(id) ON DELETE RESTRICT,
    validation_expires_at timestamptz,
    actor_id uuid NOT NULL,
    correlation_id text NOT NULL,
    receipt text NOT NULL,
    idempotency_key text NOT NULL,
    request_hash text NOT NULL,
    before_state jsonb NOT NULL DEFAULT '{}'::jsonb,
    after_state jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT confenge_cohort_candidate_reviews_idempotency UNIQUE (organization_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS confenge_cohort_candidate_reviews_latest_idx
    ON confenge_cohort_candidate_reviews (cohort_version_id, candidate_id, created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS confenge_cohort_go_decisions (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    cohort_version_id uuid NOT NULL REFERENCES confenge_cohort_versions(id) ON DELETE RESTRICT,
    decision text NOT NULL CHECK (decision IN ('GO','NO_GO')),
    reason text NOT NULL,
    readiness_hash text NOT NULL,
    authorization_id uuid,
    actor_id uuid NOT NULL,
    correlation_id text NOT NULL,
    receipt text NOT NULL,
    idempotency_key text NOT NULL,
    request_hash text NOT NULL,
    before_state jsonb NOT NULL DEFAULT '{}'::jsonb,
    after_state jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT confenge_cohort_go_decisions_idempotency UNIQUE (organization_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS confenge_cohort_go_decisions_latest_idx
    ON confenge_cohort_go_decisions (cohort_version_id, created_at DESC, id DESC);
