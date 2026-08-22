-- Migration 000092 pinned authorization_mode to '', HUMAN_TOUCHPOINT_APPROVAL and
-- CAMPAIGN_POLICY. The bounded cohort operator writes BOUNDED_COHORT_AUTHORIZATION,
-- so every frozen-cohort touchpoint insert/update failed the check constraint.
ALTER TABLE outreach_touchpoints
    DROP CONSTRAINT IF EXISTS outreach_touchpoints_auth_mode_check;

ALTER TABLE outreach_touchpoints
    ADD CONSTRAINT outreach_touchpoints_auth_mode_check
        CHECK (authorization_mode IN (
            '', 'HUMAN_TOUCHPOINT_APPROVAL', 'CAMPAIGN_POLICY', 'BOUNDED_COHORT_AUTHORIZATION'
        ));

COMMENT ON COLUMN outreach_touchpoints.authorization_mode IS
'HUMAN_TOUCHPOINT_APPROVAL requires approved_by; CAMPAIGN_POLICY uses a policy grant and must leave approved_by null; BOUNDED_COHORT_AUTHORIZATION is a frozen founder-authorized cohort grant.';
