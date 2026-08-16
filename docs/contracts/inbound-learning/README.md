# inbound-learning contracts

Versioned commercial events for Warmbly issue #47.

- `event-schema.md` — envelope `confenge.commercial_event.v1` and types
- `INTEGRATION_NOTES.md` — manifesto: versions plus web-cfg and extra-cli consumers
- `fixtures/events.v1.json` — labeled SYNTHETIC sequences

Report schema: `confenge.inbound_learning_report.v1`.
Intel tables: migration `000101` (schema) + `000102` (exception codes).

Ingest path: `intel.IngestEvent` (CLI `confenge intel-report`, HTTP
`POST /confenge/intel/events`). Real events replace fixtures without
a schema redesign. Warmbly does not rewrite web-cfg attribution or
extra-cli facts, and never writes upstream.
