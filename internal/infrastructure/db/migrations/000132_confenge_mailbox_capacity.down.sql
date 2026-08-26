DROP INDEX IF EXISTS confenge_dispatch_failures_mailbox_idx;
DROP INDEX IF EXISTS confenge_dispatch_failures_task_code_uidx;
DROP INDEX IF EXISTS confenge_dispatch_queue_mailbox_idx;
DROP INDEX IF EXISTS confenge_dispatch_sends_mailbox_idx;
DROP INDEX IF EXISTS confenge_dispatch_reservations_mailbox_idx;

ALTER TABLE confenge_dispatch_failures
    DROP CONSTRAINT IF EXISTS confenge_dispatch_failures_error_class_check,
    DROP COLUMN IF EXISTS error_class,
    DROP COLUMN IF EXISTS error_code,
    DROP COLUMN IF EXISTS task_id,
    DROP COLUMN IF EXISTS email_account_id;

ALTER TABLE confenge_dispatch_queue
    DROP COLUMN IF EXISTS email_account_id;

ALTER TABLE confenge_dispatch_sends
    DROP COLUMN IF EXISTS task_id,
    DROP COLUMN IF EXISTS email_account_id;

ALTER TABLE confenge_dispatch_reservations
    DROP COLUMN IF EXISTS attempted_at,
    DROP COLUMN IF EXISTS task_id,
    DROP COLUMN IF EXISTS email_account_id;
