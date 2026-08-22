DROP INDEX IF EXISTS confenge_dossier_references_org_attached_idx;

ALTER TABLE confenge_dossier_references
    DROP CONSTRAINT IF EXISTS confenge_dossier_references_dossier_id_bounds,
    DROP CONSTRAINT IF EXISTS confenge_dossier_references_content_hash_bounds,
    DROP CONSTRAINT IF EXISTS confenge_dossier_references_public_hash_bounds,
    DROP CONSTRAINT IF EXISTS confenge_dossier_references_as_of_bounds,
    DROP CONSTRAINT IF EXISTS confenge_dossier_references_producer_sha_bounds,
    DROP CONSTRAINT IF EXISTS confenge_dossier_references_artifact_uri_bounds,
    DROP CONSTRAINT IF EXISTS confenge_dossier_references_delivery_note_bounds,
    DROP CONSTRAINT IF EXISTS confenge_dossier_references_not_deliverable_reason_bounds;
