DROP TRIGGER IF EXISTS confenge_touchpoint_authorization_grant_check ON outreach_touchpoints;
DROP FUNCTION IF EXISTS confenge_check_touchpoint_authorization_grant();

-- Clear references the narrower single-table FK cannot satisfy before restoring it.
UPDATE outreach_touchpoints
   SET campaign_policy_authorization_id = NULL
 WHERE campaign_policy_authorization_id IS NOT NULL
   AND campaign_policy_authorization_id NOT IN (
       SELECT id FROM confenge_campaign_policy_authorizations
   );

ALTER TABLE outreach_touchpoints
    ADD CONSTRAINT outreach_touchpoints_cpa_fk
        FOREIGN KEY (campaign_policy_authorization_id)
        REFERENCES confenge_campaign_policy_authorizations(id) ON DELETE SET NULL;
