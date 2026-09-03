-- CONTRACT_CLAIM_ATTESTATION/1.0: additive per-account attestation envelope.
-- Free-form, evolving, read-then-execute blob imported from extra-cli;
-- absence preserves current behavior (fail-closed at the dispatch gate, not
-- at ingest). Never filtered or joined in SQL, so jsonb is the correct shape.
ALTER TABLE outreach_accounts
    ADD COLUMN IF NOT EXISTS claim_attestation_json jsonb;
