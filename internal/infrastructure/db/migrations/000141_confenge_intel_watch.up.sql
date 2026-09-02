-- INTEL_WATCH: a contact asks to be told when one specific subject changes.
-- Consent provenance travels on the subscription row, so a later send can prove
-- why the address is being written to instead of inferring it.

CREATE TABLE IF NOT EXISTS confenge_intel_watch_subscriptions (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id       uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    contact_email         text NOT NULL,
    contact_id            uuid REFERENCES outreach_contact_candidates(id) ON DELETE SET NULL,
    account_id            uuid REFERENCES outreach_accounts(id) ON DELETE SET NULL,
    intent_kind           text NOT NULL,
    subject_key           text NOT NULL,
    topic                 text NOT NULL DEFAULT '',
    cadence               text NOT NULL DEFAULT 'immediate',
    consent_source        text NOT NULL DEFAULT '',
    consent_at            timestamptz,
    consent_provenance_ok boolean NOT NULL DEFAULT false,
    unsubscribed_at       timestamptz,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    CHECK (contact_email <> ''),
    CHECK (subject_key <> ''),
    CHECK (intent_kind IN ('NEW_OPPORTUNITY', 'OPPORTUNITY_CHANGED', 'DEADLINE_CHANGED', 'FIT_BECAME_RELEVANT')),
    CHECK (cadence IN ('immediate', 'daily', 'weekly'))
);

-- Re-subscribing is idempotent: one row per contact per subject per intent.
CREATE UNIQUE INDEX IF NOT EXISTS confenge_intel_watch_subscriptions_identity_idx
    ON confenge_intel_watch_subscriptions (organization_id, contact_email, subject_key, intent_kind);

-- The consumer's only read path: active watchers of one subject.
CREATE INDEX IF NOT EXISTS confenge_intel_watch_subscriptions_subject_idx
    ON confenge_intel_watch_subscriptions (organization_id, subject_key)
    WHERE unsubscribed_at IS NULL;

-- The delivery ledger. Dedup is a database fact, not an application race: the
-- composite primary key is the delivery identity, so two consumers racing the
-- same event contend on one row instead of both sending.
--
-- A single "already sent" row cannot tell a delivery that was never attempted
-- apart from one the watcher received, so the row carries state: PENDING (a
-- claimed attempt, reclaimable once its lease expires because nothing was handed
-- over), IN_FLIGHT (the dispatcher is past its point of no return, so the row is
-- never reclaimed), DISPATCHED (terminal success, the only state that means
-- "nothing changed => nothing sent"), FAILED (terminal), AMBIGUOUS (unknown
-- outcome, parked for review and never auto-retried).
CREATE TABLE IF NOT EXISTS confenge_intel_watch_dedup (
    subscription_id uuid NOT NULL REFERENCES confenge_intel_watch_subscriptions(id) ON DELETE CASCADE,
    event_identity  text NOT NULL,
    content_hash    text NOT NULL,
    state           text NOT NULL DEFAULT 'PENDING',
    attempts        integer NOT NULL DEFAULT 0,
    claimed_at      timestamptz,
    lease_until     timestamptz,
    sent_at         timestamptz,
    last_error      text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (subscription_id, event_identity, content_hash),
    CHECK (event_identity <> ''),
    CHECK (content_hash <> ''),
    CHECK (state IN ('PENDING', 'IN_FLIGHT', 'DISPATCHED', 'FAILED', 'AMBIGUOUS')),
    -- A delivery may only claim a send time once it actually completed one.
    CHECK (state <> 'DISPATCHED' OR sent_at IS NOT NULL)
);

-- The reconciler's only read path: fenced attempts whose worker went away.
CREATE INDEX IF NOT EXISTS confenge_intel_watch_dedup_stale_handoff_idx
    ON confenge_intel_watch_dedup (lease_until)
    WHERE state = 'IN_FLIGHT';
