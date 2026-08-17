# EVIDENCE — CONFENGE-WARMBLY-OFFER-REVENUE-04

## Contract / schema / migration

- Extended `confenge.commercial_event.v1` (no fork, no second ledger).
- Additive migration `000103_outreach_intel_offer_revenue` widens exception CHECK, adds `outreach_intel_event_receipts` and `outreach_intel_capacity_holds`. Down restores 000102 CHECK and drops the new tables.
- Frozen catalog: `schemas/catalog.freeze.v1.json`. Authority hash override: `CONFENGE_CATALOG_AUTHORITY_HASH`. Extra R$10k is not a catalog row.
- Capacity policy `capacity.policy.v1` limit 50, hold TTL 72h.

## State machine / invariants (shipped `ApplyCommercialTransition` + `IngestEvent`)

```
DIAG_COMPLETE received=800000 contracted=800000 mrr=0 onboarded=true held=false
DIR180_END received_count=6 received=9000000 ended=true seventh_held=true
CREATED_NOT_REVENUE received=0 created_status=created
CALLBACK_NO_FINANCE status= received=0 held=true
NO_CAPACITY held=true
HOLD_EXPIRED held=true
EXTRA_PRIVATE held=true catalog_has_extra=false
WON_UNKNOWN outcome=UNKNOWN
```

## Exceptions / replay / race

```
DUP_PROVIDER replay=true
CONFLICT_EXT held=true
OOO_PAYMENT held=true
UNKNOWN_EVENT held=true raw=TELEPORTED canonical=UNKNOWN
UNAVAILABLE held=true exceptions=1
EX_RESOLVE_REOPEN resolve=deferred reopen=open
CAPACITY_RACE ok=50 fail=10 held=50 available=0
RECEIPT_RACE created=1
```

`go test -race` on capacity/receipt/diagnostico/180/finance: ok.

## Finance (pure, no provider mutation)

```
FINANCE penalty=30000 interest=7500 ipca_missing=true early=1000000 waiver=0 review=true
STARTED_MONTHS n=2
```

2% penalty, 1%/month simple pro rata die, IPCA only when versioned input is supplied, early-exit min(cálculo, 20% commitment, unpaid), waiver needs actor+evidence, always `finance_review_required=true`.

## Manual-first

```
MANUAL_FIRST timeline=4 received=800000 onboarded=true pii_rejected=true
OPERATOR_UNAUTH status=400
OPERATOR_HTTP status=201
```

Authenticated routes only. Adapter is not called. Onboarding before the financial gate is held.

## Executive / #55 latencies

```
EXEC real_empty=true syn_received=9800000 syn_mrr=0 syn_contracted=9800000 pay_lat=39540000 causal=false
```

`include_synthetic=0` stays empty. Contracted, MRR, and received are separate fields. `causal_proof=false`. Latency fields `lead_to_payment_ms`, `payment_to_onboarding_ms`, `onboarding_to_activation_ms` live on the existing executive payload (no second store).

## Suite

- suite1 and suite2: intel + confenge + handler targeted tests ok
- `go test -count=2` on finance/diagnostico/180/executive/manual/webhook: ok
- `gofmt -l` empty on touched Go
- golangci-lint on intel/confenge/handler/api: pass

## Follow-up gates (unsourced payment / PG capacity)

```
RX_NO_SNAPSHOT held=true received=0 real=0 syn=0
WEBHOOK_NO_REVENUE acked=true held=true received=0 real_empty=true synthetic=true
PG_CAPACITY_STORE hold_err="intel pg store unavailable" limit=50 table=true
```

`payment_received` uses the same prior-snapshot/checkout gate as `payment_confirmed`. Sandbox `MapEvent` sets `synthetic=true` and does not pre-apply received cents. Held chains do not increment received/MRR/contracted. `PGStore` implements `CapacityStore` on `outreach_intel_capacity_holds`.

## Deploy / canary

Not deployed from this environment. Adapter default is sandbox/disabled. No production Asaas key. No public real-money mutation. `LIVE_PROVEN` is BLOCKED.

## Classification

| Label | Status |
| --- | --- |
| CODE_PROVEN | yes |
| CI_PROVEN | pending remote CI |
| MERGED | no (at write time) |
| DEPLOYED | no |
| LIVE_PROVEN | no |
| CONTROLLED_OWNER_CANARY | no (this SHA) |
| SANDBOX | yes |
| REAL_EXTERNAL | no |
| BLOCKED | live/deploy |
| UNKNOWN | live host |
| NO_GO | no for code; live is not claimed |
