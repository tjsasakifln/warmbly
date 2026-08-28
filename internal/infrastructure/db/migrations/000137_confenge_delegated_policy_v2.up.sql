-- CFG-FIRST-TOUCH-ROUTING-v2 is the live authorized policy. The original
-- delegated batch/decision tables pinned v1 only, so autorun could evaluate
-- v2 and then fail closed on INSERT.
ALTER TABLE confenge_delegated_first_touch_batches
    DROP CONSTRAINT IF EXISTS confenge_delegated_batch_policy_v1;
ALTER TABLE confenge_delegated_first_touch_batches
    ADD CONSTRAINT confenge_delegated_batch_policy_v1
        CHECK (policy_version IN ('CFG-FIRST-TOUCH-ROUTING-v1', 'CFG-FIRST-TOUCH-ROUTING-v2'));

ALTER TABLE confenge_delegated_first_touch_decisions
    DROP CONSTRAINT IF EXISTS confenge_delegated_decision_policy_v1;
ALTER TABLE confenge_delegated_first_touch_decisions
    ADD CONSTRAINT confenge_delegated_decision_policy_v1
        CHECK (policy_version IN ('CFG-FIRST-TOUCH-ROUTING-v1', 'CFG-FIRST-TOUCH-ROUTING-v2'));
