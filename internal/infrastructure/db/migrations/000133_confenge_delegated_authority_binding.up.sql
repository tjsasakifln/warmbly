-- Bind delegated approvals to the exact producer authority that authorized them.
ALTER TABLE confenge_delegated_first_touch_batches
    ADD COLUMN IF NOT EXISTS source_expires_at timestamptz,
    ADD COLUMN IF NOT EXISTS source_freshness_hash text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS target_membership_hash text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS target_membership_count integer NOT NULL DEFAULT 0;

ALTER TABLE confenge_delegated_first_touch_decisions
    ADD COLUMN IF NOT EXISTS source_expires_at timestamptz,
    ADD COLUMN IF NOT EXISTS source_freshness_hash text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS target_membership_hash text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS target_membership_count integer NOT NULL DEFAULT 0;

ALTER TABLE confenge_delegated_first_touch_batches
    ADD CONSTRAINT confenge_delegated_batch_source_freshness_hash_check
        CHECK (source_freshness_hash = '' OR source_freshness_hash ~ '^[a-f0-9]{64}$'),
    ADD CONSTRAINT confenge_delegated_batch_target_membership_hash_check
        CHECK (target_membership_hash = '' OR target_membership_hash ~ '^[a-f0-9]{64}$'),
    ADD CONSTRAINT confenge_delegated_batch_target_membership_count_check
        CHECK (target_membership_count >= 0);

ALTER TABLE confenge_delegated_first_touch_decisions
    ADD CONSTRAINT confenge_delegated_decision_source_freshness_hash_check
        CHECK (source_freshness_hash = '' OR source_freshness_hash ~ '^[a-f0-9]{64}$'),
    ADD CONSTRAINT confenge_delegated_decision_target_membership_hash_check
        CHECK (target_membership_hash = '' OR target_membership_hash ~ '^[a-f0-9]{64}$'),
    ADD CONSTRAINT confenge_delegated_decision_target_membership_count_check
        CHECK (target_membership_count >= 0);

CREATE UNIQUE INDEX IF NOT EXISTS confenge_delegated_first_touch_live_content_uidx
    ON confenge_delegated_first_touch_decisions
        (organization_id, evidence_source_run_id, content_hash)
    WHERE decision = 'DELEGATED_POLICY_APPROVE'
      AND state IN ('APPROVED','QUEUED','SENT','APPROVED_NOT_SCHEDULED')
      AND content_hash <> '';
