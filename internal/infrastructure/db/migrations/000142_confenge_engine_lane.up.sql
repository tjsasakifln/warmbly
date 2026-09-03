-- Engine attribution for the CONFENGE multi-engine acquisition surface.
--
-- This is deliberately NOT the existing `lane` column. `lane` is the operator
-- cockpit's queue routing (EMAIL_NEEDS_REVIEW, INBOUND_NOW, ...) and several
-- code paths switch on it; overloading it would corrupt queue placement.
-- `engine_lane` answers a different question: which acquisition engine
-- produced this signal, so a founder can tell which engine is actually working
-- without the four being merged into one aggregate.
--
-- Empty string is the honest default for every pre-existing row: those signals
-- predate engine attribution and must not be silently claimed by any engine.
ALTER TABLE outreach_commercial_actions
    ADD COLUMN IF NOT EXISTS engine_lane text NOT NULL DEFAULT '';

COMMENT ON COLUMN outreach_commercial_actions.engine_lane IS
'Acquisition engine of origin: outbound_first_touch | intel_seed | intel_watch | confenge_web. Empty means unattributed (pre-dates engine attribution); never inferred.';

CREATE INDEX IF NOT EXISTS outreach_commercial_actions_engine_lane_idx
    ON outreach_commercial_actions (organization_id, engine_lane)
    WHERE engine_lane <> '';
