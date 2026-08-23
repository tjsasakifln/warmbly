-- Human-gate derivation taxonomy.
--
-- Every confenge_cohort_versions row is now explicit about HOW it came to
-- exist, because "version N+1 exists" alone cannot answer the only question
-- that matters at audit time: is this the same bytes, the same composer, or a
-- human's edit?
--
--   CREATE    first freeze of a cohort from a canonical source run
--   REPRODUCE frozen_manifest copied byte-for-byte from the parent version
--   RECOMPOSE reserved: re-run the composer against current source/policy
--             /composer. Not implemented yet; the taxonomy is durable first so
--             existing rows never have to be reinterpreted later.
--   ADJUST    a human edited subject/body for exactly one candidate; every
--             other member is copied unchanged and hashes are recomputed.
--
-- parent_version names the version this row was derived from, within the same
-- (organization_id, cohort_id). It is NULL only for CREATE.

ALTER TABLE confenge_cohort_versions
    ADD COLUMN IF NOT EXISTS derivation text NOT NULL DEFAULT 'CREATE';

ALTER TABLE confenge_cohort_versions
    ADD COLUMN IF NOT EXISTS parent_version integer;

-- Backfill: rows written before this migration are either a byte-for-byte
-- reproduction (they carry reproduced_from_version) or an original freeze.
UPDATE confenge_cohort_versions
   SET derivation = 'REPRODUCE'
 WHERE reproduced_from_version IS NOT NULL
   AND derivation = 'CREATE';

UPDATE confenge_cohort_versions
   SET parent_version = reproduced_from_version
 WHERE reproduced_from_version IS NOT NULL
   AND parent_version IS NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'confenge_cohort_versions_derivation_check'
    ) THEN
        ALTER TABLE confenge_cohort_versions
            ADD CONSTRAINT confenge_cohort_versions_derivation_check
            CHECK (derivation IN ('CREATE','REPRODUCE','RECOMPOSE','ADJUST'));
    END IF;
END
$$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'confenge_cohort_versions_parent_check'
    ) THEN
        ALTER TABLE confenge_cohort_versions
            ADD CONSTRAINT confenge_cohort_versions_parent_check
            CHECK (
                (derivation = 'CREATE' AND parent_version IS NULL)
                OR (derivation <> 'CREATE' AND parent_version IS NOT NULL AND parent_version < version)
            );
    END IF;
END
$$;

-- Per-candidate audit of every human copy adjustment. One row per accepted
-- adjust; the immutable before/after hashes make the edit provable without
-- re-reading either manifest.
CREATE TABLE IF NOT EXISTS confenge_cohort_adjustments (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    cohort_id uuid NOT NULL,
    from_version_id uuid NOT NULL REFERENCES confenge_cohort_versions(id) ON DELETE RESTRICT,
    to_version_id uuid NOT NULL REFERENCES confenge_cohort_versions(id) ON DELETE RESTRICT,
    from_version integer NOT NULL CHECK (from_version > 0),
    to_version integer NOT NULL CHECK (to_version > from_version),
    candidate_id uuid NOT NULL,
    before_content_hash text NOT NULL,
    after_content_hash text NOT NULL,
    before_frozen_hash text NOT NULL,
    after_frozen_hash text NOT NULL,
    diff jsonb NOT NULL DEFAULT '[]'::jsonb,
    reason text NOT NULL,
    revoked_authorization_id uuid,
    actor_id uuid NOT NULL,
    correlation_id text NOT NULL,
    receipt text NOT NULL,
    idempotency_key text NOT NULL,
    request_hash text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT confenge_cohort_adjustments_idempotency UNIQUE (organization_id, idempotency_key),
    CONSTRAINT confenge_cohort_adjustments_target UNIQUE (to_version_id, candidate_id)
);

CREATE INDEX IF NOT EXISTS confenge_cohort_adjustments_cohort_idx
    ON confenge_cohort_adjustments (organization_id, cohort_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS confenge_cohort_adjustments_from_idx
    ON confenge_cohort_adjustments (from_version_id, created_at DESC, id DESC);
