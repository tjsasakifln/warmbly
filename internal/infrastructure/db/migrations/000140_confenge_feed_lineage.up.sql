-- A manifest run becomes readable only after the whole sync closes. Chunk import
-- rows are not publication proof because a later chunk or backlog stage can fail.
CREATE TABLE IF NOT EXISTS outreach_feed_committed_runs (
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    source_run_id text NOT NULL,
    snapshot_hash text NOT NULL,
    committed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, source_run_id),
    CHECK (source_run_id <> ''),
    CHECK (snapshot_hash <> '')
);

-- Seed the current last-good run.
INSERT INTO outreach_feed_committed_runs (organization_id, source_run_id, snapshot_hash, committed_at)
SELECT organization_id, last_run_id, last_snapshot_hash, last_success_at
FROM outreach_feed_sync_state
WHERE last_success_at IS NOT NULL AND last_run_id <> '' AND last_snapshot_hash <> ''
ON CONFLICT (organization_id, source_run_id) DO NOTHING;

-- Never infer historical receipts. An older carryover without an explicit
-- committed-run row remains blocked until a complete replay publishes one.

-- Normalize provider-accepted work first. The ledger is external truth even
-- when legacy projections still say queued, reserved or attempted.
UPDATE confenge_dispatch_reservations r
SET state='committed',committed_at=COALESCE(r.committed_at,sent.sent_at),last_error=''
FROM confenge_dispatch_sends sent
WHERE r.organization_id=sent.organization_id AND r.message_key=sent.message_key
  AND r.state='reserved';

UPDATE confenge_dispatch_queue q
SET status='sent',cancel_reason='',last_error='',reserved_until=NULL,updated_at=now()
FROM confenge_dispatch_sends sent
WHERE q.organization_id=sent.organization_id
  AND (q.message_key=sent.message_key OR q.id=sent.queue_id)
  AND q.status IN ('queued','reserved','attempted');

UPDATE outreach_touchpoints t
SET state='SENT',sent_at=COALESCE(t.sent_at,sent.sent_at),
    provider_message_id=CASE WHEN t.provider_message_id='' THEN sent.provider_message_id ELSE t.provider_message_id END,
    stop_reason='',delegated_reserved_until=NULL,delegated_last_error='',updated_at=now()
FROM confenge_dispatch_sends sent
WHERE t.organization_id=sent.organization_id AND (
    t.draft_id=sent.draft_id OR EXISTS (
      SELECT 1 FROM confenge_dispatch_queue q
      WHERE q.organization_id=sent.organization_id AND q.id=sent.queue_id AND q.draft_id=t.draft_id
    ))
  AND t.ordinal=1 AND t.purpose='INITIAL' AND t.channel='EMAIL'
  AND t.state IN ('PLANNED','DUE','DRAFTED','AI_REWRITE_PENDING','ENRICHMENT_PENDING',
    'REJECTED_REWRITE_PENDING','NEEDS_REVIEW','APPROVED','QUEUED');

UPDATE outreach_drafts d
SET status='SENT',updated_at=now()
FROM confenge_dispatch_sends sent
WHERE d.organization_id=sent.organization_id AND (
    d.id=sent.draft_id OR EXISTS (
      SELECT 1 FROM confenge_dispatch_queue q
      WHERE q.organization_id=sent.organization_id AND q.id=sent.queue_id AND q.draft_id=d.id
    )) AND d.status<>'SENT';

UPDATE confenge_delegated_first_touch_decisions decision
SET state='SENT',sent_at=COALESCE(decision.sent_at,sent.sent_at),updated_at=now()
FROM confenge_dispatch_sends sent
WHERE decision.organization_id=sent.organization_id AND (
    decision.draft_id=sent.draft_id OR EXISTS (
      SELECT 1 FROM confenge_dispatch_queue q
      WHERE q.organization_id=sent.organization_id AND q.id=sent.queue_id AND q.draft_id=decision.draft_id
    ))
  AND decision.state IN ('APPROVED','QUEUED','APPROVED_NOT_SCHEDULED');

-- Release open reservations and cancel replayable work whose source run has no
-- committed receipt. Attempted remains terminal and is never made replayable.
UPDATE confenge_dispatch_reservations r
SET state='released',last_error='feed_lineage_uncommitted'
FROM confenge_dispatch_queue q
JOIN outreach_touchpoints t
  ON t.organization_id=q.organization_id AND t.draft_id=q.draft_id
WHERE r.organization_id=q.organization_id AND r.message_key=q.message_key AND r.state='reserved'
  AND q.status IN ('queued','reserved')
  AND NOT EXISTS (
    SELECT 1 FROM outreach_feed_committed_runs committed
    WHERE committed.organization_id=t.organization_id AND committed.source_run_id=t.source_run_id
  );

UPDATE confenge_dispatch_queue q
SET status='cancelled',cancel_reason='feed_lineage_uncommitted',
    last_error='feed_lineage_uncommitted',reserved_until=NULL,updated_at=now()
FROM outreach_touchpoints t
WHERE t.organization_id=q.organization_id AND t.draft_id=q.draft_id
  AND q.status IN ('queued','reserved')
  AND NOT EXISTS (
    SELECT 1 FROM outreach_feed_committed_runs committed
    WHERE committed.organization_id=t.organization_id AND committed.source_run_id=t.source_run_id
  );

UPDATE confenge_delegated_first_touch_decisions decision
SET state='CANCELLED',blocker_codes=CASE WHEN blocker_codes ? 'feed_lineage_uncommitted' THEN blocker_codes
  ELSE blocker_codes || '["feed_lineage_uncommitted"]'::jsonb END,updated_at=now()
FROM outreach_touchpoints t
WHERE decision.organization_id=t.organization_id AND decision.touchpoint_id=t.id
  AND decision.state IN ('APPROVED','QUEUED','APPROVED_NOT_SCHEDULED')
  AND NOT EXISTS (SELECT 1 FROM confenge_dispatch_queue q WHERE q.organization_id=t.organization_id AND q.draft_id=t.draft_id AND q.status='attempted')
  AND NOT EXISTS (
    SELECT 1 FROM outreach_feed_committed_runs committed
    WHERE committed.organization_id=t.organization_id AND committed.source_run_id=t.source_run_id
  );

UPDATE outreach_drafts d
SET status='BLOCKED',approved_by=NULL,approved_at=NULL,campaign_id=NULL,
    enrollment_contact_id=NULL,enrolled_at=NULL,updated_at=now()
FROM outreach_touchpoints t
WHERE d.organization_id=t.organization_id AND d.id=t.draft_id AND d.status<>'SENT'
  AND t.state IN ('PLANNED','DUE','DRAFTED','AI_REWRITE_PENDING','ENRICHMENT_PENDING',
    'REJECTED_REWRITE_PENDING','NEEDS_REVIEW','APPROVED','QUEUED')
  AND NOT EXISTS (SELECT 1 FROM confenge_dispatch_queue q WHERE q.organization_id=t.organization_id AND q.draft_id=t.draft_id AND q.status='attempted')
  AND NOT EXISTS (
    SELECT 1 FROM outreach_feed_committed_runs committed
    WHERE committed.organization_id=t.organization_id AND committed.source_run_id=t.source_run_id
  );

UPDATE outreach_touchpoints t
SET state='CANCELLED',stop_reason='feed_lineage_uncommitted',approved_content_hash='',
    approved_by=NULL,approved_at=NULL,authorization_mode='',campaign_policy_authorization_id=NULL,
    authorization_policy_hash='',authorization_at=NULL,signature_version='',queued_at=NULL,
    delegated_reserved_until=NULL,delegated_last_error='feed_lineage_uncommitted',updated_at=now()
WHERE t.ordinal=1 AND t.purpose='INITIAL' AND t.channel='EMAIL'
  AND t.state IN ('PLANNED','DUE','DRAFTED','AI_REWRITE_PENDING','ENRICHMENT_PENDING',
    'REJECTED_REWRITE_PENDING','NEEDS_REVIEW','APPROVED','QUEUED')
  AND NOT EXISTS (SELECT 1 FROM confenge_dispatch_queue q WHERE q.organization_id=t.organization_id AND q.draft_id=t.draft_id AND q.status='attempted')
  AND NOT EXISTS (
    SELECT 1 FROM outreach_feed_committed_runs committed
    WHERE committed.organization_id=t.organization_id AND committed.source_run_id=t.source_run_id
  );

-- One common database predicate for every SQL boundary. The Go transport
-- boundary additionally verifies the evidence hash and derived window.
CREATE OR REPLACE FUNCTION confenge_commercially_qualified(
    qualification_state text,
    qualified_until date,
    deactivated boolean,
    as_of date
) RETURNS boolean
LANGUAGE sql
IMMUTABLE
PARALLEL SAFE
AS $$
    SELECT upper(coalesce(qualification_state, '')) = 'QUALIFIED'
       AND qualified_until IS NOT NULL
       AND qualified_until > as_of
       AND NOT coalesce(deactivated, false)
$$;
