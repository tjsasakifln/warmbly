# Acquisition outcome feedback

`confenge.acquisition_outcome_feedback.v1` is Warmbly's read-only economic
feedback export for acquisition cohorts. It answers at the safely joinable
acquisition-context level and exposes the precise gap that currently prevents
route/intent detail. It is not a lead export, CRM view, causal claim, or write
back into `web-cfg`.

## Read contract

`GET /confenge/intel/outcome-feedback` requires `READ_CONTACTS` and accepts:

- `from`: optional first UTC cohort month (`YYYY-MM`), inclusive;
- `to`: optional last UTC cohort month (`YYYY-MM`), inclusive;
- `include_synthetic`: optional, false by default.

With no period parameters, the export returns the current and previous two
calendar months. The response exposes the normalized half-open bounds and each
row carries its fixed `cohort_month`. Arbitrary dates/timestamps are rejected,
so overlapping queries cannot create narrower cohorts by differencing.
Cohort membership uses the persisted lead creation timestamp, falling back to
the chain creation timestamp only when the lead timestamp is absent. The GET
reads existing commercial-intelligence chains and never materializes or
refreshes them.

Rows contain only normalized producer source, closed organic source, closed
route family, closed asset family, cohort month, record kind, and aggregate
commercial facts. Raw landing path, asset ID, CTA ID, intent class, and buyer
job are returned as `UNKNOWN` because the current inbound contract has no
closed, versioned aggregate taxonomy for them. Names, email, phone, CNPJ,
company, free text, documents, recipient identity, receipt IDs, lead IDs,
correlation IDs, account IDs, proposal IDs, and payment IDs are never exported.

## Stage semantics

The stages are not interchangeable:

- `receipts` counts distinct persisted receipt IDs;
- `leads` counts distinct persisted lead IDs;
- `qualified_opportunities` requires explicit `QUALIFIED_CONVERSATION` plus an
  opaque opportunity ID. A validated lead, even with an opportunity ID, is not
  QCO;
- `proposals` counts only proposal facts already projected onto the commercial
  chain;
- `contracts` remains `UNJOINABLE` because Warmbly does not currently persist
  a dedicated evidence-backed contract state;
- `outcomes.won` and `outcomes.lost` require human-confirmed outcome evidence
  and are not relabeled as contracts;
- `known_value` sums only persisted contracted and received cents. It is an
  observed lower bound, not forecast or contract count;
- `known_margin` remains `UNKNOWN` because no evidence-backed margin fact is
  persisted.

Every stage includes an availability status. Records without an observed fact
stay `UNKNOWN`; absence is never changed to zero. `causal_proof` is always
false.

## Small-cell privacy

The minimum direct cell size is five privacy units. A privacy unit is resolved
conservatively from an already-persisted stable account ID and is never
returned. Person, receipt, and lead IDs do not prove independent accounts and
never increase the threshold. Rows without a stable account join contribute
zero privacy units and therefore cannot satisfy the threshold.

- Direct closed-context/month/record-kind cells with at least five units are
  returned.
- Smaller direct cells are combined into one dimensionless
  `SMALL_CELL_ROLLUP` row.
- If the combined roll-up is still smaller than five, all counts and values are
  null with status `WITHHELD_SMALL_CELL`.

The threshold is fixed in v1 and cannot be reduced by a query parameter.
QCO, proposal, WON/LOST, and value facts also require five contributing privacy
units inside an otherwise publishable cell. A positive count from one to four,
its complementary unknown count, and all associated value are null with status
`WITHHELD_SMALL_CELL`. Synthetic and real records are separate record-kind
cells and roll-ups, so synthetic subjects can never unsuppress a real subject.

## Current join gaps

- Route attribution is `PARTIAL`: normalized source, organic source, route
  family, and the three existing closed asset families are safe. Raw landing
  path, asset ID, CTA ID, and intent class remain `UNJOINABLE` / `UNKNOWN`
  because their persisted strings are not backed by a closed versioned
  aggregate taxonomy.
- `buyer_job` is not persisted and is therefore `UNJOINABLE`.
- Bare `lead_validated` state is not a qualified opportunity. QCO coverage is
  partial until a dedicated QCO event/ID is present.
- Native `confenge_proposals` are not read by this projection. Only proposal
  evidence already on the commercial chain is visible, so proposal coverage is
  `PARTIAL`.
- Dedicated contract state and evidence-backed margin are not persisted.

These gaps are returned under `join_gaps`; they are not repaired by guessing or
joining on PII.

## Future web-cfg producer contract

No `web-cfg` change is part of this contract. A future versioned producer
change may improve route and intent coverage by sending closed, versioned
registry identifiers already owned by the public acquisition plane:

- `source=CONFENGE_WEB`, stable `lead_id`, `receipt_id`, and `correlation_id`;
- a route-family registry ID and version instead of a raw landing path;
- closed asset and CTA registry IDs plus their registry version;
- a closed intent registry ID plus its version, never a raw search query;
- a public-family-registry `buyer_job` slug only after the inbound schema and
  Warmbly storage explicitly version and accept it;
- consent, page, content, CTA, asset, and record-kind versions.

Do not send raw route/query/referrer URLs, form message, name, email, phone, CNPJ,
company, document content, or recipient identity. Until a versioned buyer-job
field and closed route/asset/CTA/intent registries exist on both sides, the
export must continue returning `UNKNOWN` / `UNJOINABLE` for those dimensions.
