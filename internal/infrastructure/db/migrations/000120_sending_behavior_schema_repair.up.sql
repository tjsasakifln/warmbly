-- Repair historical databases whose migration ledger advanced past 81 while
-- the sending-behaviour objects were absent. Every statement is additive so
-- healthy databases remain unchanged.

CREATE TABLE IF NOT EXISTS email_account_behavior (
    email_account_id  uuid PRIMARY KEY REFERENCES email_accounts(id) ON DELETE CASCADE,
    enabled           boolean NOT NULL DEFAULT false,
    daily_limit_min   integer NOT NULL DEFAULT 30,
    daily_limit_max   integer NOT NULL DEFAULT 45,
    hourly_limit_min  integer NOT NULL DEFAULT 5,
    hourly_limit_max  integer NOT NULL DEFAULT 9,
    gap_min_seconds   integer NOT NULL DEFAULT 90,
    gap_max_seconds   integer NOT NULL DEFAULT 420,
    work_start_min    integer NOT NULL DEFAULT 543,
    work_start_max    integer NOT NULL DEFAULT 567,
    work_end_min      integer NOT NULL DEFAULT 1038,
    work_end_max      integer NOT NULL DEFAULT 1076,
    lunch_enabled     boolean NOT NULL DEFAULT true,
    lunch_earliest    integer NOT NULL DEFAULT 720,
    lunch_latest      integer NOT NULL DEFAULT 810,
    lunch_min_minutes integer NOT NULL DEFAULT 30,
    lunch_max_minutes integer NOT NULL DEFAULT 60,
    weekdays          smallint NOT NULL DEFAULT 31,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT behavior_daily_range  CHECK (daily_limit_min >= 1 AND daily_limit_max <= 500 AND daily_limit_min <= daily_limit_max),
    CONSTRAINT behavior_hourly_range CHECK (hourly_limit_min >= 1 AND hourly_limit_max <= 200 AND hourly_limit_min <= hourly_limit_max),
    CONSTRAINT behavior_gap_range    CHECK (gap_min_seconds >= 30 AND gap_max_seconds <= 86400 AND gap_min_seconds <= gap_max_seconds),
    CONSTRAINT behavior_start_range  CHECK (work_start_min >= 0 AND work_start_max <= 1439 AND work_start_min <= work_start_max),
    CONSTRAINT behavior_end_range    CHECK (work_end_min >= 0 AND work_end_max <= 1439 AND work_end_min <= work_end_max),
    CONSTRAINT behavior_day_order    CHECK (work_start_max < work_end_min),
    CONSTRAINT behavior_lunch_window CHECK (lunch_earliest >= 0 AND lunch_latest <= 1439 AND lunch_earliest <= lunch_latest),
    CONSTRAINT behavior_lunch_length CHECK (lunch_min_minutes >= 0 AND lunch_max_minutes <= 240 AND lunch_min_minutes <= lunch_max_minutes),
    CONSTRAINT behavior_weekdays     CHECK (weekdays >= 0 AND weekdays <= 127)
);

CREATE TABLE IF NOT EXISTS email_account_daily_plan (
    email_account_id   uuid NOT NULL REFERENCES email_accounts(id) ON DELETE CASCADE,
    plan_date          date NOT NULL,
    timezone           text NOT NULL,
    is_working_day     boolean NOT NULL,
    daily_limit        integer NOT NULL,
    hourly_limit       integer NOT NULL,
    work_start_minute  integer NOT NULL,
    work_end_minute    integer NOT NULL,
    lunch_start_minute integer,
    lunch_end_minute   integer,
    gap_min_seconds    integer NOT NULL,
    gap_max_seconds    integer NOT NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (email_account_id, plan_date),
    CONSTRAINT plan_lunch_pair CHECK (
        (lunch_start_minute IS NULL AND lunch_end_minute IS NULL)
        OR (lunch_start_minute IS NOT NULL AND lunch_end_minute IS NOT NULL
            AND lunch_start_minute < lunch_end_minute)
    )
);

CREATE INDEX IF NOT EXISTS idx_email_account_daily_plan_date
    ON email_account_daily_plan (plan_date);

ALTER TABLE campaigns
    ADD COLUMN IF NOT EXISTS guardrail_enabled boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS guardrail_bounce_rate_max numeric(5,2) NOT NULL DEFAULT 5.00,
    ADD COLUMN IF NOT EXISTS guardrail_complaint_rate_max numeric(5,2) NOT NULL DEFAULT 0.10,
    ADD COLUMN IF NOT EXISTS guardrail_reply_rate_min numeric(5,2) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS guardrail_min_sample integer NOT NULL DEFAULT 50,
    ADD COLUMN IF NOT EXISTS guardrail_window_days integer NOT NULL DEFAULT 7,
    ADD COLUMN IF NOT EXISTS guardrail_tripped_at timestamptz,
    ADD COLUMN IF NOT EXISTS guardrail_reason text NOT NULL DEFAULT '';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = 'campaigns'::regclass
          AND conname = 'campaigns_guardrail_bounds'
    ) THEN
        ALTER TABLE campaigns
            ADD CONSTRAINT campaigns_guardrail_bounds CHECK (
                guardrail_bounce_rate_max >= 0 AND guardrail_bounce_rate_max <= 100 AND
                guardrail_complaint_rate_max >= 0 AND guardrail_complaint_rate_max <= 100 AND
                guardrail_reply_rate_min >= 0 AND guardrail_reply_rate_min <= 100 AND
                guardrail_min_sample >= 1 AND guardrail_min_sample <= 100000 AND
                guardrail_window_days >= 0 AND guardrail_window_days <= 365
            );
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_campaigns_guardrail_active
    ON campaigns (id) WHERE guardrail_enabled;

ALTER TYPE public.campaign_status ADD VALUE IF NOT EXISTS 'paused_guardrail';
