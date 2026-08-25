-- Recover only import-derived strict-lane blocks from the fully applied
-- authoritative snapshot. This prepares candidates for human review; it does
-- not approve, schedule, queue, enroll, resume or send anything.
UPDATE outreach_contact_candidates c
SET blocked = false,
    block_reason = '',
    updated_at = now()
FROM outreach_import_runs ir,
     outreach_feed_sync_state fs
WHERE ir.id = c.last_import_run_id
  AND ir.organization_id = c.organization_id
  AND fs.organization_id = c.organization_id
  AND fs.last_status = 'completed'
  AND fs.last_snapshot_hash <> ''
  AND fs.last_run_id <> ''
  AND ir.status = 'completed'
  AND ir.snapshot_hash = fs.last_snapshot_hash
  AND ir.source_run_id = fs.last_run_id
  AND c.blocked = true
  AND c.block_reason IN ('published_exhausted', 'provenance_chain_invalid')
  AND c.do_not_contact = false
  AND c.bounced = false
  AND c.discovery_json @> '{"controlled_email_eligible":true}'::jsonb
  AND upper(COALESCE(c.discovery_json->>'route_class','')) IN (
      'DIRECT_PERSON', 'ROLE_OR_DEPARTMENT', 'GENERIC_COMPANY', 'PUBLIC_COMPANY_FREEMAIL'
  )
  AND upper(COALESCE(c.discovery_json->>'mailbox_company_evidence','')) = 'OBSERVED'
  AND upper(COALESCE(c.discovery_json->>'risk_class','')) = 'ALLOWED'
  AND upper(COALESCE(c.channel_epistemic_class,'')) = 'OBSERVED'
  AND upper(COALESCE(c.route_freshness,'')) = 'FRESH'
  AND upper(COALESCE(c.route_suppression,'')) = 'NONE'
  AND upper(COALESCE(c.ownership_status,'')) = 'COMPANY_OWNED'
  AND upper(COALESCE(c.verification_status,'')) = 'OFFICIAL_SOURCE'
  AND c.email ~* '^[A-Z0-9.!#$%&''*+/=?^_`{|}~-]+@[A-Z0-9](?:[A-Z0-9-]{0,61}[A-Z0-9])?(?:\.[A-Z0-9](?:[A-Z0-9-]{0,61}[A-Z0-9])?)+$'
  AND lower(c.email) !~ '(^|@)(test|demo|fixture|fake|synthetic|example)'
  AND lower(c.email) !~ '@demo[0-9]*obra\.com\.br$'
  AND lower(COALESCE(c.source_url,'')) !~ '(fixture|/fixtures/|example\.(com|org|net)|demo[0-9]*obra|warmbly\.local|localhost|127\.0\.0\.1|synthetic|fake-contact)';
