-- Proposal v1 is Warmbly-owned commercial state. It does not create checkout,
-- payment, billing, or delivery rows.

CREATE TABLE IF NOT EXISTS confenge_proposals (
    organization_id        uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    proposal_id            uuid NOT NULL,
    proposal_version       integer NOT NULL,
    account_id             text NOT NULL,
    client_ref             text NOT NULL,
    opportunity_id         text NOT NULL,
    qco_id                 text NOT NULL,
    deal_id                text,
    correlation_id         text NOT NULL,
    offer_id               text NOT NULL,
    offer_version          text NOT NULL,
    deliverable_id         text NOT NULL,
    deliverable_version    text NOT NULL,
    scope_version          text NOT NULL,
    price_version          text NOT NULL,
    terms_version          text NOT NULL,
    decision_state         text NOT NULL,
    accepted_snapshot_hash text,
    synthetic              boolean NOT NULL DEFAULT false,
    record_version         bigint NOT NULL,
    valid_until            timestamptz NOT NULL,
    payload                jsonb NOT NULL,
    created_at             timestamptz NOT NULL,
    updated_at             timestamptz NOT NULL,
    PRIMARY KEY (organization_id, proposal_id, proposal_version),
    CONSTRAINT confenge_proposals_version_check CHECK (proposal_version > 0 AND record_version > 0),
    CONSTRAINT confenge_proposals_state_check CHECK (decision_state IN (
        'DRAFT', 'PREPARED', 'APPROVED_TO_SEND', 'SENT', 'NEGOTIATING',
        'ACCEPTED', 'REJECTED', 'EXPIRED', 'UNKNOWN'
    )),
    CONSTRAINT confenge_proposals_accept_hash_check CHECK (
        (decision_state = 'ACCEPTED' AND accepted_snapshot_hash LIKE 'sha256:%') OR
        (decision_state <> 'ACCEPTED' AND accepted_snapshot_hash IS NULL)
    )
);

CREATE INDEX IF NOT EXISTS confenge_proposals_account_idx
    ON confenge_proposals (organization_id, account_id, updated_at DESC);

CREATE INDEX IF NOT EXISTS confenge_proposals_opportunity_idx
    ON confenge_proposals (organization_id, opportunity_id, qco_id);

CREATE TABLE IF NOT EXISTS confenge_proposal_events (
    event_id          uuid PRIMARY KEY,
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    proposal_id       uuid NOT NULL,
    proposal_version  integer NOT NULL,
    event_type        text NOT NULL,
    idempotency_key   text NOT NULL,
    occurred_at       timestamptz NOT NULL,
    payload           jsonb NOT NULL,
    FOREIGN KEY (organization_id, proposal_id, proposal_version)
        REFERENCES confenge_proposals (organization_id, proposal_id, proposal_version)
        ON DELETE RESTRICT,
    UNIQUE (organization_id, idempotency_key, event_type)
);

CREATE TABLE IF NOT EXISTS confenge_proposal_command_receipts (
    organization_id  uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    idempotency_key   text NOT NULL,
    payload_hash      text NOT NULL,
    proposal_id       uuid NOT NULL,
    proposal_version  integer NOT NULL,
    payload           jsonb NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, idempotency_key),
    FOREIGN KEY (organization_id, proposal_id, proposal_version)
        REFERENCES confenge_proposals (organization_id, proposal_id, proposal_version)
        ON DELETE RESTRICT
);

CREATE OR REPLACE FUNCTION confenge_proposal_accepted_immutable()
RETURNS trigger AS $$
BEGIN
    IF OLD.decision_state = 'ACCEPTED' AND NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'accepted proposal version is immutable';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER confenge_proposal_accepted_immutable_trigger
BEFORE UPDATE ON confenge_proposals
FOR EACH ROW EXECUTE FUNCTION confenge_proposal_accepted_immutable();

COMMENT ON TABLE confenge_proposals IS
'Warmbly-owned immutable proposal versions. Accepted rows may only be superseded by proposal_version + 1.';
COMMENT ON TABLE confenge_proposal_events IS
'Proposal state and reference-only delivery request events. No email or money side effects.';
