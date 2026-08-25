# CONFENGE proposal v1

Warmbly owns proposal versions and commercial decision state under
`tjsasakifln/warmbly#47`. Governance owns readiness, capacity, Work Orders and
delivery state. web-cfg owns public offer and contracting producers. These
contracts do not create a CRM, checkout, charge, billing ledger or Work Order.

## State and immutability

The implemented path is:

`DRAFT -> PREPARED -> APPROVED_TO_SEND -> SENT -> NEGOTIATING -> ACCEPTED | REJECTED | EXPIRED | UNKNOWN`

Direct `SENT -> ACCEPTED | REJECTED | EXPIRED | UNKNOWN` is also valid when no
negotiation event occurred. `ACCEPTED` freezes a canonical SHA-256 snapshot.
The accepted row cannot be updated. A material change appends
`proposal_version + 1` in `DRAFT` while retaining the accepted version.
`amount` uses minor currency units.

Every command has an organization-scoped idempotency receipt. Reusing the key
with another payload fails closed. State writes use optimistic record versions.
Proposal and event rows are durable Postgres state from migration `000125`.
The service validates contract lengths and currency shape before persistence.

## Delivery handoff

Acceptance emits a stable `confenge.delivery_order_requested.v1` with
`financial_gate.state=UNKNOWN`. Governance holds it. An explicit later command
can emit another request for the same accepted business key with either:

- `SYNTHETIC_VALID`, only from a labeled fixture and always
  `received_revenue=false`;
- `AUTHORIZED`, only with a non-synthetic source event and explicit evidence.

The future real replacement for the synthetic source is the Warmbly-owned
`confenge.financial_gate_reconciled.v1`. HTTP success, callback receipt,
`PAYMENT_CONFIRMED`, checkout creation and the nested gate never prove received
revenue.

Delivery event identity uses a fixed-length SHA-256 digest of the accepted
business key, financial state and source event. Long valid business IDs cannot
overflow the published idempotency-key limit.

## Reproducible fixture

The synthetic canary uses redacted identities and performs no network or money
side effects:

```sh
go run ./cmd/confenge-proposal-canary
```

Its committed output is
`fixtures/delivery-order-requested.synthetic.v1.json`.

The accepted proposal fixture is reproducible with
`go run ./cmd/confenge-proposal-canary --proposal`.

Focused verification:

```sh
go test ./internal/app/confenge/proposal -count=1
```
