-- extra-cli decision-unit person_id is distinct from source_contact_id
-- (candidate_id). Persist it so CollectToday re-plan cannot drop identity.
ALTER TABLE outreach_contact_candidates
    ADD COLUMN IF NOT EXISTS person_id text NOT NULL DEFAULT '';

ALTER TABLE outreach_commercial_actions
    ADD COLUMN IF NOT EXISTS person_id text NOT NULL DEFAULT '';

COMMENT ON COLUMN outreach_contact_candidates.person_id IS
'Imported extra-cli decision-unit person_id. Never invent; never collapse into source_contact_id.';
COMMENT ON COLUMN outreach_commercial_actions.person_id IS
'Copied from the imported extra-cli person_id when published. Distinct from source_contact_id.';
