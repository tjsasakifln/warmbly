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
performs SELECT-only reads of existing commercial-intelligence chains and
native `confenge_proposals`; it never reconciles, refreshes, or materializes a
read model.

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
- `proposals` deduplicates chain proposal IDs with native proposal IDs. A native
  proposal is attached only when one eligible chain matches the same
  organization-scoped opaque correlation, account, and opportunity; an
  observed `source_lead_id`, when present, must also match. Ambiguous,
  conflicting, held, synthetic-mismatched, unsafe, or unjoinable rows remain
  unknown;
- native `DRAFT`, `PREPARED`, and `APPROVED_TO_SEND` rows do not satisfy the
  proposal stage. The projector chooses the latest version with observed
  `sent_at` per proposal ID and accepts only `SENT`, `NEGOTIATING`, `ACCEPTED`,
  `REJECTED`, `EXPIRED`, or the observed terminal state `UNKNOWN`;
- `proposal_states` exposes only state cells with five contributing accounts;
  smaller state cells are omitted and marked withheld;
- `proposed_value.by_currency` sums the observed native proposal amount in
  minor currency units only when that currency has five contributing accounts.
  It is quote value, never contracted value, received value, contract, or
  margin;
- `contracts` remains `UNJOINABLE` because Warmbly does not currently persist
  a dedicated evidence-backed contract state;
- `outcomes.won` and `outcomes.lost` require human-confirmed outcome evidence
  and are not relabeled as contracts;
- `known_value.by_currency` keeps persisted contracted and received cents in
  separate currency buckets. Each currency and each value stage needs five
  contributors independently. Mixed currencies are never numerically summed;
  overflow or malformed currency fails closed to unknown. These values are an
  observed lower bound, not forecast or contract count;
- `known_margin` remains `UNKNOWN` because no evidence-backed margin fact is
  persisted.

Every stage includes an availability status. `WON` and `LOST`, contracted and
received value, proposal state, and proposed value are suppressed
independently, so one small substage cannot hide another substage that already
has five contributors. Records without an observed fact stay `UNKNOWN`;
absence is never changed to zero. Held commercial chains never contribute QCO,
proposal, outcome, contracted/received value, or proposed value.
`causal_proof` is always false.

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
Receipt, lead, QCO, proposal, each proposal state, each proposed-value currency,
WON, LOST, and each contracted/received currency stage require five contributing
privacy units inside an otherwise publishable cell. The unknown complement is
computed from contributing accounts, never from the number of IDs or events.
When a substage has one to four contributors, its count, complement, and value
are null with status `WITHHELD_SMALL_CELL`; other independently safe substages
remain visible. Synthetic and real records are separate record-kind cells and
roll-ups, so synthetic subjects can never unsuppress a real subject.

## Coverage matrix

| Field or stage | Coverage | Persisted authority and rule |
| --- | --- | --- |
| normalized source | `OBSERVED` when present | exact allowed `CONFENGE_WEB` producer only |
| organic source | `OBSERVED` or `UNKNOWN` | closed Warmbly organic-source taxonomy |
| route family | `OBSERVED` | exact closed `inbound` family is required together with `CONFENGE_WEB` |
| asset family | `OBSERVED` or `UNKNOWN` | only the three closed persisted families |
| receipt / lead | `OBSERVED`, `UNKNOWN`, or withheld | distinct persisted IDs; privacy threshold uses account contributors |
| QCO | `PARTIAL` | explicit `QUALIFIED_CONVERSATION` plus opportunity ID only |
| proposal | `PARTIAL` | chain facts plus strict native proposal join; ambiguous/unjoinable rows stay unknown |
| proposal state / proposed value | `PARTIAL` | latest observed sent native version; state and minor-unit currency cells use independent k=5 |
| WON / LOST | `PARTIAL` | human-confirmed, non-held outcome only; independently suppressed |
| contracted / received value | `PARTIAL` | existing evidence-backed chain values, separated by currency and independently suppressed |
| contract | `UNJOINABLE` | no dedicated persisted client-contract state |
| margin | `UNKNOWN` | no evidence-backed cost or margin fact |
| raw route, asset ID, CTA, intent, buyer job | `UNJOINABLE` | no closed versioned producer registry on the inbound contract |

## Current join gaps

- Route attribution is `PARTIAL`: normalized source, organic source, route
  family, and the three existing closed asset families are safe. Raw landing
  path, asset ID, CTA ID, and intent class remain `UNJOINABLE` / `UNKNOWN`
  because their persisted strings are not backed by a closed versioned
  aggregate taxonomy.
- `buyer_job` is not persisted and is therefore `UNJOINABLE`.
- Bare `lead_validated` state is not a qualified opportunity. QCO coverage is
  partial until a dedicated QCO event/ID is present.
- Native `confenge_proposals` are now read, but proposal coverage remains
  `PARTIAL`: only exact, safe, unique chain associations and observed sent
  versions are eligible. Account-only, opportunity-only, unsafe, ambiguous,
  conflicting, held, draft, and synthetic-mismatched candidates are not joined.
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
