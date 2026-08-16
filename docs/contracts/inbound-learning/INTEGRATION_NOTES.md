# inbound-learning integration manifesto

Warmbly issue #47 is the residual consumer of two sibling planes. This
note is the contract both producers must keep. It is not a second
schema and not a write path back to either plane.

## Versions

| Artifact | Version | Authority |
| --- | --- | --- |
| commercial event envelope | `confenge.commercial_event.v1` | producer (`version` or `schema`) |
| inbound learning report | `confenge.inbound_learning_report.v1` | Warmbly (`GET /confenge/intel/report`, `confenge intel-report`) |
| executive join payload | `confenge.commercial_intel.v1` | Warmbly (`GET /confenge/intel/executive`) |
| intel tables | migration `000101` (schema) + `000102` (exception codes) | Warmbly |

Fixtures and future real events share one ingest path:
`intel.IngestEvent`, HTTP `POST /confenge/intel/events`, CLI
`confenge intel-report`. Same `event_id` or `idempotency_key` is a
replay. Labeled SYNTHETIC never increments causal proof, an SLO, or an
observed business metric. `--include-synthetic=false` on a fixture-only
store is zeros / `UNKNOWN` / `real_empty` and must not emit
`BASELINE_OBSERVED`.

## web-cfg consumer (source / query / asset / CTA / consent / lead)

web-cfg owns attribution. Warmbly copies pointers; it does not
rederive source, query, asset, CTA, consent, or lead identity.

Minimum for a real inbound lead:

- `event_id`, `version` or `schema`, `type=lead_received`
- `occurred_at`, `timezone`
- `lead_id` (stable), `correlation_id`
- `source`, `query`, `asset_id`, `cta_id` when known
- `asset_family` when the surface is Market Answer, Contract Analysis,
  or B2G X-Ray, plus `market_answer_id` or `analysis_id`
- `consent` as a pointer, never a raw email/phone/CNPJ
- `pii_pointer` if a PII vault row exists
- `producer_sha`

Follow-up events (`lead_validated`, `handoff_accepted`,
`action_approved`, `action_executed`, `reply`, `meeting`, `proposal`,
`pipeline_created`, `won` / `lost` / `unknown`, `revenue_evidenced`)
must reuse `lead_id` (or `receipt_id`) so they join the first chain.

What web-cfg must not send:

- contract text, article body, public narrative
- raw email, phone, name, CNPJ in metric fields
- inferred WON/LOST or inferred revenue
- page view / citation / X-Ray completion labeled as a lead

Warmbly must not rewrite web-cfg attribution. Missing source/query/asset
stays UNKNOWN (`missing_attribution`). A page view, citation, or X-Ray
completion is `NotALead` and cannot become pipeline.

## extra-cli consumer (facts / entities / events / evidence)

extra-cli owns facts, entities, events, evidence, and intelligence
watermarks. Warmbly copies public refs and versions; it does not
rederive target-fit, activation, entity identity, or evidence.

What extra-cli should send on a commercial event (when known):

- `account_public_id` / `entity_public_id` (public refs only)
- `person_id` only as an already-stable extra-cli id, never a name
- `target_fit_version`, `activation_policy_version`,
  `target_fit_watermark`, `target_fit_fresh` when an account is present
- extra-cli `event` ids as pointers in `evidence_ref` / `event_id`
  material, not as a second lead identity
- outbound / partner / expansion `route_family` when the path is not
  inbound (do not label outbound as inbound)

What extra-cli must not send:

- dossier prose, contract text, trigger narrative, or evidence body
- CNPJ, email, phone, or person name in metric fields
- inferred WON/LOST, inferred pipeline, or inferred revenue
- a write-back instruction; Warmbly never PATCHes extra-cli

What Warmbly must not rewrite on extra-cli fields:

- first `account_id` wins; a disagreeing account is
  `conflicting_account` and is not overwritten
- missing versions stay UNKNOWN (`missing_version`); Warmbly does not
  invent a watermark
- learning verdicts (`REPEAT` / `CHANGE` / `STOP` / `NEED_MORE_DATA`)
  stay local `LEARNING_CANDIDATE` rows
- `upstream_writes` is always empty (no extra-cli, web-cfg, or SmartLic
  write)

## Authority

| Plane | Owns | Warmbly must not |
| --- | --- | --- |
| extra-cli | facts, entities, events, evidence, intelligence | rewrite account, versions, evidence, or write upstream |
| web-cfg | source, query, asset, CTA, consent, lead attribution | rewrite attribution or invent a lead |
| Warmbly | approved action, delivery, reply, outcome, next action | AUTO_SEND, identity/consent inference, bulk approval |
| finance / delivery | revenue and service evidence | increment revenue without `revenue_document_id` |

## Swap fixture for real event

1. Keep `CONFENGE_AUTO_SEND_ENABLED=false`.
2. POST one real `lead_received` (not `synthetic`, not qa/internal).
3. Query `GET /confenge/intel/report?include_synthetic=0`.
4. Expect the chain to reconstruct from `lead_id`. Latency baseline
   becomes `BASELINE_OBSERVED` only after a non-synthetic event.
5. Do not close Warmbly #47 until a real action/outcome/pipeline or
   an observed rejection exists.

Idempotent replacement: the same `event_id` is a replay. A new real
`event_id` with the same `lead_id` merges onto the first chain.
