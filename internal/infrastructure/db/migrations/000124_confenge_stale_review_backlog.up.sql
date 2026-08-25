-- A NEEDS_REVIEW draft bound to a recipient absent from the newest account
-- snapshot cannot be approved. Retire only those unapproved first touches and
-- return accounts with another current eligible route to draft generation.
WITH stale_accounts AS MATERIALIZED (
  SELECT DISTINCT t.account_id,t.organization_id
  FROM outreach_touchpoints t
  JOIN outreach_accounts a
    ON a.organization_id=t.organization_id AND a.id=t.account_id
  LEFT JOIN outreach_contact_candidates c
    ON c.organization_id=t.organization_id AND c.id=t.contact_candidate_id
  WHERE t.ordinal=1 AND t.state='NEEDS_REVIEW'
    AND (a.last_import_run_id IS NULL OR c.id IS NULL OR c.last_import_run_id IS DISTINCT FROM a.last_import_run_id)
)
UPDATE outreach_drafts d
SET status='BLOCKED',approved_by=NULL,approved_at=NULL,
    campaign_id=NULL,enrollment_contact_id=NULL,enrolled_at=NULL,updated_at=now()
FROM stale_accounts
WHERE d.organization_id=stale_accounts.organization_id
  AND d.account_id=stale_accounts.account_id
  AND d.status IN ('NOT_GENERATED','GENERATING','AI_REWRITE_PENDING','ENRICHMENT_PENDING','REJECTED_REWRITE_PENDING','NEEDS_REVIEW');

WITH stale_accounts AS MATERIALIZED (
  SELECT DISTINCT t.account_id,t.organization_id
  FROM outreach_touchpoints t
  JOIN outreach_accounts a
    ON a.organization_id=t.organization_id AND a.id=t.account_id
  LEFT JOIN outreach_contact_candidates c
    ON c.organization_id=t.organization_id AND c.id=t.contact_candidate_id
  WHERE t.ordinal=1 AND t.state='NEEDS_REVIEW'
    AND (a.last_import_run_id IS NULL OR c.id IS NULL OR c.last_import_run_id IS DISTINCT FROM a.last_import_run_id)
)
UPDATE outreach_touchpoints t
SET state='CANCELLED',stop_reason='recipient_not_in_current_snapshot',
    approved_content_hash='',approved_by=NULL,approved_at=NULL,
    authorization_mode='',campaign_policy_authorization_id=NULL,
    authorization_policy_hash='',authorization_at=NULL,signature_version='',
    queued_at=NULL,updated_at=transaction_timestamp()
FROM stale_accounts
WHERE t.organization_id=stale_accounts.organization_id
  AND t.account_id=stale_accounts.account_id
  AND t.state IN ('PLANNED','DUE','DRAFTED','AI_REWRITE_PENDING','ENRICHMENT_PENDING','REJECTED_REWRITE_PENDING','NEEDS_REVIEW');

UPDATE outreach_accounts a
SET queue_state='NEEDS_CONTACT',draft_generation_reserved_until=NULL,
    draft_generation_last_error='recipient_not_in_current_snapshot',updated_at=now()
WHERE a.queue_state='NEEDS_REVIEW'
  AND EXISTS (
    SELECT 1 FROM outreach_touchpoints retired
    WHERE retired.organization_id=a.organization_id AND retired.account_id=a.id
      AND retired.ordinal=1 AND retired.state='CANCELLED'
      AND retired.stop_reason='recipient_not_in_current_snapshot'
      AND retired.updated_at=transaction_timestamp()
  )
  AND NOT EXISTS (
    SELECT 1 FROM outreach_touchpoints active
    WHERE active.organization_id=a.organization_id AND active.account_id=a.id
      AND active.state IN ('AI_REWRITE_PENDING','ENRICHMENT_PENDING','REJECTED_REWRITE_PENDING','NEEDS_REVIEW','APPROVED','QUEUED')
  );

UPDATE outreach_accounts a
SET queue_state='READY_TO_GENERATE',draft_generation_retry_at=now(),
    draft_generation_reserved_until=NULL,draft_generation_last_error='',updated_at=now()
WHERE a.queue_state='NEEDS_CONTACT'
  AND a.target_fit_eligible=true AND a.target_fit_fresh=true
  AND a.target_fit_class='TARGET_CONFIRMED'
  AND a.blocked=false AND a.do_not_contact=false
  AND EXISTS (
    SELECT 1 FROM outreach_touchpoints retired
    WHERE retired.organization_id=a.organization_id AND retired.account_id=a.id
      AND retired.ordinal=1 AND retired.state='CANCELLED'
      AND retired.stop_reason='recipient_not_in_current_snapshot'
      AND retired.updated_at=transaction_timestamp()
  )
  AND EXISTS (
    SELECT 1 FROM outreach_contact_candidates c
    WHERE c.organization_id=a.organization_id AND c.account_id=a.id
      AND a.last_import_run_id IS NOT NULL AND c.last_import_run_id=a.last_import_run_id
      AND c.email<>'' AND c.blocked=false AND c.do_not_contact=false AND c.bounced=false
      AND (
        (c.email_send_ready=true AND c.mailbox_purpose_send_blocked=false
         AND c.verification_status NOT IN ('CANDIDATE_UNVERIFIED','NOT_FOUND','INVALID','BOUNCED','DO_NOT_CONTACT'))
        OR (c.discovery_json @> '{"controlled_email_eligible":true}'::jsonb
         AND upper(COALESCE(c.discovery_json->>'route_class','')) IN
         ('DIRECT_PERSON','ROLE_OR_DEPARTMENT','GENERIC_COMPANY','PUBLIC_COMPANY_FREEMAIL'))
      )
  );
