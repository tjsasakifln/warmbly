# Fragment: NET_NEW_INBOUND_HANDRAISER error codes

target_path: docs/content/docs/api/error-codes.mdx
operation: insert after the CONFENGE_WEB_INTENT/1.0 section
stable_key: NET_NEW_INBOUND_HANDRAISER/1.0.0-draft.20260904
depends_on: REV-03 consumer
test: go test ./internal/app/confenge -run 'TestDecideNetNewInboundFailClosed|TestNetNewRejects'
rollback: drop this section; consumer still fail-closes on unknown hash

## `NET_NEW_INBOUND_HANDRAISER/1.0.0-draft.20260904`

HMAC `POST /api/v1/webhooks/confenge/inbound`. HTTP 2xx is not acceptance.
`outcome` is `ACCEPTED`, `REJECTED_WITH_REASON`, or `UNKNOWN`.

| `code` / `reason` | Meaning |
| --- | --- |
| `schema_hash_unpinned` | `content_hash` / `schema_hash` / `policy_hash` missing, or runtime pin empty |
| `schema_hash_mismatch` | Hash is not the pinned content hash |
| `schema_version_unknown` | Family prefix matched, version is not the pinned draft |
| `schema_mismatch` | Body is not this contract |
| `contract_id_mismatch` | `contract_id` / `policy_id` is not `NET_NEW_INBOUND_HANDRAISER` |
| `source_not_confenge_web` | `source` is not `CONFENGE_WEB` |
| `lane_not_confenge_web` | `lane` is not `CONFENGE_WEB` / `confenge_web` |
| `consent_missing` | Consent granted + source + timestamp missing |
| `conflict_decline` | Conflict status `DECLINE`. Receipt stored, no hand-raiser |
| `conflict_unknown` | Conflict status `UNKNOWN`. Outcome `UNKNOWN` |
| `logical_id_missing` | No durable logical id |
| `nucleus_unknown` | Nucleus outside the taxonomy closed set |
| `intel_watch_not_handraiser` | INTEL_WATCH / Live Intelligence source on this envelope |
| `downstream_unavailable` | Store or queue write failed after receipt persist |
| `readback_stale` | Receipt exists but the consume did not finish |
| `inbound_store_unavailable` | `503`. Receipt could not be persisted |
