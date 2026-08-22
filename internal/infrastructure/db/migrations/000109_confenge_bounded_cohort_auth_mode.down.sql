-- Revert to the pre-bounded-cohort set. Rows already carrying the bounded mode
-- are reset to '' so the narrower constraint can be re-applied.
UPDATE outreach_touchpoints
   SET authorization_mode = ''
 WHERE authorization_mode = 'BOUNDED_COHORT_AUTHORIZATION';

ALTER TABLE outreach_touchpoints
    DROP CONSTRAINT IF EXISTS outreach_touchpoints_auth_mode_check;

ALTER TABLE outreach_touchpoints
    ADD CONSTRAINT outreach_touchpoints_auth_mode_check
        CHECK (authorization_mode IN (
            '', 'HUMAN_TOUCHPOINT_APPROVAL', 'CAMPAIGN_POLICY'
        ));
