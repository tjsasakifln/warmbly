DROP INDEX IF EXISTS confenge_dispatch_queue_attempted_idx;

-- Collapsing 'attempted' back into 'sent' re-asserts an acceptance nobody
-- observed. The rollback keeps the distinction visible by folding it into
-- 'failed', which is the honest reading of "handed over, never confirmed".
UPDATE confenge_dispatch_queue
SET status = 'failed', last_error = COALESCE(NULLIF(last_error, ''), 'attempted_acceptance_unknown')
WHERE status = 'attempted';

ALTER TABLE confenge_dispatch_queue
    DROP CONSTRAINT IF EXISTS confenge_dispatch_queue_status_check;

ALTER TABLE confenge_dispatch_queue
    ADD CONSTRAINT confenge_dispatch_queue_status_check
    CHECK (status IN ('queued', 'reserved', 'sent', 'cancelled', 'failed'));
