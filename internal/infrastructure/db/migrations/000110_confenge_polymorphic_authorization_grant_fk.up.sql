-- outreach_touchpoints.campaign_policy_authorization_id is a polymorphic grant
-- reference discriminated by authorization_mode. The single-table foreign key
-- was only correct while CAMPAIGN_POLICY was the sole non-empty mode; a
-- BOUNDED_COHORT_AUTHORIZATION grant lives in a different table and always
-- failed outreach_touchpoints_cpa_fk. Replace the FK with a mode-aware trigger
-- so both grant types keep referential integrity.
ALTER TABLE outreach_touchpoints
    DROP CONSTRAINT IF EXISTS outreach_touchpoints_cpa_fk;

CREATE OR REPLACE FUNCTION confenge_check_touchpoint_authorization_grant()
RETURNS trigger AS $$
BEGIN
    IF NEW.campaign_policy_authorization_id IS NULL THEN
        RETURN NEW;
    END IF;

    IF NEW.authorization_mode = 'BOUNDED_COHORT_AUTHORIZATION' THEN
        IF NOT EXISTS (
            SELECT 1 FROM confenge_bounded_cohort_authorizations
             WHERE id = NEW.campaign_policy_authorization_id
        ) THEN
            RAISE EXCEPTION
                'bounded cohort grant % not found for touchpoint %',
                NEW.campaign_policy_authorization_id, NEW.id
                USING ERRCODE = 'foreign_key_violation';
        END IF;
        RETURN NEW;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM confenge_campaign_policy_authorizations
         WHERE id = NEW.campaign_policy_authorization_id
    ) THEN
        RAISE EXCEPTION
            'campaign policy grant % not found for touchpoint %',
            NEW.campaign_policy_authorization_id, NEW.id
            USING ERRCODE = 'foreign_key_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS confenge_touchpoint_authorization_grant_check ON outreach_touchpoints;

CREATE TRIGGER confenge_touchpoint_authorization_grant_check
    BEFORE INSERT OR UPDATE OF campaign_policy_authorization_id, authorization_mode
    ON outreach_touchpoints
    FOR EACH ROW
    EXECUTE FUNCTION confenge_check_touchpoint_authorization_grant();

COMMENT ON COLUMN outreach_touchpoints.campaign_policy_authorization_id IS
'Polymorphic grant reference. authorization_mode selects the table: CAMPAIGN_POLICY -> confenge_campaign_policy_authorizations, BOUNDED_COHORT_AUTHORIZATION -> confenge_bounded_cohort_authorizations. Enforced by confenge_touchpoint_authorization_grant_check.';
