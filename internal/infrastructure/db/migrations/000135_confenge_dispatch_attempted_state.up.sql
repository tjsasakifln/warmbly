-- The dispatch queue had no state between "queued" and "sent", so the worker
-- marked a row 'sent' the moment it handed the message to the transport. The
-- code comment said as much ("QueueSent means handed to the configured
-- transport"), but the stored value asserted something stronger than anything
-- the system had observed.
--
-- Production showed the gap plainly: confenge_dispatch_queue held six rows in
-- 'sent' while confenge_dispatch_sends -- the provider-fact table -- was empty
-- and no touchpoint had ever reached SENT. Nothing corroborated the claim.
--
-- 'attempted' is the honest terminal state for a hand-off. A row is promoted to
-- 'sent' only when a provider fact exists. Acceptance that was never observed
-- stays UNKNOWN instead of being asserted.

ALTER TABLE confenge_dispatch_queue
    DROP CONSTRAINT IF EXISTS confenge_dispatch_queue_status_check;

ALTER TABLE confenge_dispatch_queue
    ADD CONSTRAINT confenge_dispatch_queue_status_check
    CHECK (status IN ('queued', 'reserved', 'attempted', 'sent', 'cancelled', 'failed'));

-- Reconcile the history that exists rather than fabricate one. A row keeps
-- 'sent' only where a provider fact corroborates it; everything else is demoted
-- to the state it actually reached. This never invents a send and never deletes
-- one.
UPDATE confenge_dispatch_queue q
SET status = 'attempted'
WHERE q.status = 'sent'
  AND NOT EXISTS (
      SELECT 1 FROM confenge_dispatch_sends s
      WHERE s.draft_id IS NOT DISTINCT FROM q.draft_id
        AND s.organization_id = q.organization_id
  )
  AND NOT EXISTS (
      SELECT 1 FROM outreach_touchpoints tp
      WHERE tp.draft_id IS NOT DISTINCT FROM q.draft_id
        AND tp.organization_id = q.organization_id
        AND tp.state = 'SENT'
  );

CREATE INDEX IF NOT EXISTS confenge_dispatch_queue_attempted_idx
    ON confenge_dispatch_queue (organization_id, updated_at)
    WHERE status = 'attempted';
