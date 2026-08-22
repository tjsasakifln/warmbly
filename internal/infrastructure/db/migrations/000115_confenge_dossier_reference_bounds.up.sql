-- Every persisted scalar on confenge_dossier_references was free-form text. The
-- application-side guard only inspected key names, so a caller could put the
-- whole dossier, and the prospect identity, inside dossier_id, as_of or
-- delivery_note. These bounds make the database refuse it too, independently of
-- the Go layer.

ALTER TABLE confenge_dossier_references
    ADD CONSTRAINT confenge_dossier_references_dossier_id_bounds
        CHECK (dossier_id ~ '^[A-Za-z0-9_.:-]{1,128}$'),
    ADD CONSTRAINT confenge_dossier_references_content_hash_bounds
        CHECK (content_hash ~ '^sha256:[0-9a-f]{64}$'),
    ADD CONSTRAINT confenge_dossier_references_public_hash_bounds
        CHECK (public_content_hash = '' OR public_content_hash ~ '^sha256:[0-9a-f]{64}$'),
    ADD CONSTRAINT confenge_dossier_references_as_of_bounds
        CHECK (as_of = '' OR as_of ~ '^\d{4}-\d{2}-\d{2}$'),
    ADD CONSTRAINT confenge_dossier_references_producer_sha_bounds
        CHECK (producer_sha = '' OR producer_sha ~ '^[0-9a-f]{7,64}$'),
    -- Postgres caps a regex repetition bound at 255 (DUPMAX), so {1,512} is not
    -- a long pattern, it is invalid SQL. It only looked fine because OR
    -- short-circuits on an empty artifact_uri and the ALTER ran on an empty
    -- table: the first real value, and any populated table, raises 2201B.
    ADD CONSTRAINT confenge_dossier_references_artifact_uri_bounds
        CHECK (artifact_uri = '' OR (length(artifact_uri) <= 512 AND artifact_uri ~ '^[A-Za-z0-9_./:-]+$')),
    ADD CONSTRAINT confenge_dossier_references_delivery_note_bounds
        CHECK (octet_length(delivery_note) <= 2000),
    ADD CONSTRAINT confenge_dossier_references_not_deliverable_reason_bounds
        CHECK (octet_length(not_deliverable_reason) <= 512);

-- The badge decorator reads the newest reference per account for the accounts on
-- the operator view. The existing index leads with account_id, which blocks the
-- ordering for that read.
CREATE INDEX IF NOT EXISTS confenge_dossier_references_org_attached_idx
    ON confenge_dossier_references (organization_id, attached_at DESC);
