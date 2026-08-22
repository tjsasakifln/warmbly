-- Scoped cohort freeze reads outreach_accounts by (organization_id, source_run_id).
-- Without this index the run predicate degrades to a full org scan.
CREATE INDEX IF NOT EXISTS outreach_accounts_org_source_run_idx
    ON outreach_accounts (organization_id, source_run_id)
    WHERE source_run_id <> '';
