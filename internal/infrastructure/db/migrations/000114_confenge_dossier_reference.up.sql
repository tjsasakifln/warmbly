-- A confenge-dossier/1.0 reference is metadata on a commercial card, never a
-- send trigger. Only manifest.json fields cross the boundary: the private
-- dossier body and the prospect identity stay in extra-cli.

CREATE TABLE IF NOT EXISTS confenge_dossier_references (
    id                     uuid PRIMARY KEY,
    organization_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    account_id             uuid NOT NULL REFERENCES outreach_accounts(id) ON DELETE CASCADE,
    commercial_action_id   uuid REFERENCES outreach_commercial_actions(id) ON DELETE SET NULL,
    touchpoint_id          uuid REFERENCES outreach_touchpoints(id) ON DELETE SET NULL,
    dossier_id             text NOT NULL,
    schema_version         text NOT NULL DEFAULT 'confenge-dossier/1.0',
    catalog_mode           text NOT NULL,
    data_state             text NOT NULL,
    as_of                  text NOT NULL DEFAULT '',
    content_hash           text NOT NULL,
    public_content_hash    text NOT NULL DEFAULT '',
    producer_sha           text NOT NULL DEFAULT '',
    artifact_uri           text NOT NULL DEFAULT '',
    deliverable            boolean NOT NULL DEFAULT false,
    not_deliverable_reason text NOT NULL DEFAULT '',
    attached_by            uuid NOT NULL,
    attached_at            timestamptz NOT NULL DEFAULT now(),
    delivered_at           timestamptz,
    delivered_by           uuid,
    delivery_note          text NOT NULL DEFAULT '',
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT confenge_dossier_references_schema_check
        CHECK (schema_version = 'confenge-dossier/1.0'),
    CONSTRAINT confenge_dossier_references_data_state_check
        CHECK (data_state IN ('DATA_READY','DATA_HOLD','DATA_REJECT')),
    CONSTRAINT confenge_dossier_references_catalog_mode_check
        CHECK (catalog_mode IN ('fixture','official_live')),
    -- Deliverable is derived, never asserted. A fixture run or anything short of
    -- DATA_READY is storable but must never be presented as ready for a client.
    CONSTRAINT confenge_dossier_references_deliverable_check
        CHECK (deliverable = (catalog_mode = 'official_live' AND data_state = 'DATA_READY')),
    -- Delivery is a human act, recorded as a pair and never inferred.
    CONSTRAINT confenge_dossier_references_delivery_pair_check
        CHECK ((delivered_at IS NULL) = (delivered_by IS NULL)),
    CONSTRAINT confenge_dossier_references_delivery_gate_check
        CHECK (delivered_at IS NULL OR deliverable)
);

CREATE UNIQUE INDEX IF NOT EXISTS confenge_dossier_references_artifact_uidx
    ON confenge_dossier_references (organization_id, account_id, dossier_id, content_hash);

CREATE INDEX IF NOT EXISTS confenge_dossier_references_account_idx
    ON confenge_dossier_references (organization_id, account_id, attached_at DESC);

CREATE INDEX IF NOT EXISTS confenge_dossier_references_action_idx
    ON confenge_dossier_references (organization_id, commercial_action_id)
    WHERE commercial_action_id IS NOT NULL;

COMMENT ON TABLE confenge_dossier_references IS
'manifest.json-only reference to a confenge-dossier/1.0 artifact. Holds no dossier body and no prospect identity beyond what outreach_accounts already stores. Attaching one is card metadata and is not a send path.';
COMMENT ON COLUMN confenge_dossier_references.deliverable IS
'Derived from catalog_mode + data_state. False means storable but never presented as ready to hand to a client.';
COMMENT ON COLUMN confenge_dossier_references.delivered_at IS
'Set only by an explicit operator act. Delivery is never inferred from attachment, outcome, or send.';
COMMENT ON COLUMN confenge_dossier_references.artifact_uri IS
'Local filesystem path or URI of the artifact directory. A pointer for the human, not a fetchable body inside Warmbly.';
