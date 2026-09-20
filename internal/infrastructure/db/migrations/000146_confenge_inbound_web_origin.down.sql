ALTER TABLE outreach_inbound_leads
    DROP CONSTRAINT IF EXISTS outreach_inbound_leads_web_origin_class_check;
ALTER TABLE outreach_inbound_leads
    DROP COLUMN IF EXISTS web_origin_class;
