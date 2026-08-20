# Search observation contract `confenge.search_observation.v1`

Warmbly consumes aggregated discovery snapshots from web-cfg. This is not a
lead, not a GSC warehouse, and not a write into web-cfg.

## Envelope

| Field | Value |
| --- | --- |
| schema | `confenge.commercial_event.v1` |
| version | `confenge.search_observation.v1` |
| type | `search_observation` |
| source | `CONFENGE_WEB` (producer identity, not `organic_search`) |

Counts (`eligible`, `appeared`, `clicked`, `engaged`) are nullable. Null is
UNKNOWN. Observed zero is not UNKNOWN. Missing aggregates stay BLOCKED/ABSENT.
Individual `query` / `query_hash` / `gsc_query` are rejected with 4xx and are
never persisted.

Consent is `not_applicable` or `aggregate`. Individual consent is not invented.

## Capability

Inbound health advertises:

```
accepted_event_versions:
  - confenge.commercial_event.v1
  - confenge.search_observation.v1
```

Accept echo: `event_id`, `accepted_version`, `receipt_id`, `persisted=true`,
`replay=true|false`, `record_kind`, `not_a_lead=true`.

Unknown version or invalid required field is 4xx and does not persist a chain.
