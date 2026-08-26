-- Nullable bindings preserve historical rows whose mailbox cannot be reconstructed safely.
ALTER TABLE confenge_dispatch_reservations
    ADD COLUMN IF NOT EXISTS email_account_id uuid REFERENCES email_accounts(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS task_id uuid REFERENCES tasks(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS attempted_at timestamptz;

ALTER TABLE confenge_dispatch_sends
    ADD COLUMN IF NOT EXISTS email_account_id uuid REFERENCES email_accounts(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS task_id uuid REFERENCES tasks(id) ON DELETE SET NULL;

ALTER TABLE confenge_dispatch_queue
    ADD COLUMN IF NOT EXISTS email_account_id uuid REFERENCES email_accounts(id) ON DELETE SET NULL;

ALTER TABLE confenge_dispatch_failures
    ADD COLUMN IF NOT EXISTS email_account_id uuid REFERENCES email_accounts(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS task_id uuid REFERENCES tasks(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS error_code text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS error_class text NOT NULL DEFAULT 'unknown';

ALTER TABLE confenge_dispatch_failures
    DROP CONSTRAINT IF EXISTS confenge_dispatch_failures_error_class_check,
    ADD CONSTRAINT confenge_dispatch_failures_error_class_check CHECK (error_class IN (
        'unknown', 'auth_failure', 'provider_interrupted', 'provider_4xx',
        'provider_5xx', 'rate_limit', 'provider_block', 'recipient_rejection'
    ));

CREATE INDEX IF NOT EXISTS confenge_dispatch_reservations_mailbox_idx
    ON confenge_dispatch_reservations (email_account_id, reserved_at DESC)
    WHERE email_account_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS confenge_dispatch_sends_mailbox_idx
    ON confenge_dispatch_sends (email_account_id, sent_at DESC)
    WHERE email_account_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS confenge_dispatch_queue_mailbox_idx
    ON confenge_dispatch_queue (email_account_id, status, due_at)
    WHERE email_account_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS confenge_dispatch_failures_mailbox_idx
    ON confenge_dispatch_failures (email_account_id, occurred_at DESC)
    WHERE email_account_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS confenge_dispatch_failures_task_code_uidx
    ON confenge_dispatch_failures (task_id, error_code)
    WHERE task_id IS NOT NULL AND error_code <> '';
