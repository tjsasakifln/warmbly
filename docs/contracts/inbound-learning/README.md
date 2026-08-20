# inbound-learning contracts

Versioned commercial events for Warmbly issue #47.

- `event-schema.md` — envelope and types
- `organic-attribution.md` — closed source taxonomy and GSC/query prohibition
- `INTEGRATION_NOTES.md` — web-cfg producer notes
- `fixtures/events.v1.json` — labeled SYNTHETIC sequences

Ingest path: `intel.IngestEvent` (CLI `confenge intel-report`, HTTP
`POST /confenge/intel/events`). Real events replace fixtures without
a schema redesign.
