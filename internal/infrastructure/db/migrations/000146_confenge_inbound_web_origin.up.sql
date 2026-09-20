ALTER TABLE outreach_inbound_leads
    ADD COLUMN IF NOT EXISTS web_origin_class text NOT NULL DEFAULT '';

ALTER TABLE outreach_inbound_leads
    DROP CONSTRAINT IF EXISTS outreach_inbound_leads_web_origin_class_check;
ALTER TABLE outreach_inbound_leads
    ADD CONSTRAINT outreach_inbound_leads_web_origin_class_check CHECK (
        web_origin_class IN ('', 'campaign', 'search_organic', 'referral', 'direct_or_unknown')
    );

COMMENT ON COLUMN outreach_inbound_leads.web_origin_class IS
    'Server-derived web acquisition evidence. It is not the canonical commercial proposal origin class.';
