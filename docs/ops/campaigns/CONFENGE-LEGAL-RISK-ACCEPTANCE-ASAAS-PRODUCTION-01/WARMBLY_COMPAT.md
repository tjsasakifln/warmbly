# WARMBLY_COMPAT — CONFENGE-LEGAL-RISK-ACCEPTANCE-ASAAS-PRODUCTION-01

Status: **PARTIAL**. Consumer is deployable as a listener. This is not live Asaas proof.

## What this SHA added

- Tests in `internal/app/confenge/intel/offer_revenue_test.go` drive shipped `IngestEvent` → `ApplyCommercialTransition` (same `DiagnosticoSequence` / `Rollup` helpers):
  - `payment_confirmed` is not received cash (`CanonicalStatus=confirmed`, `ReceivedCents=0`)
  - `payment_received` is the only cash increment (R$ 8.000,00; MRR=0 on diagnóstico)
  - `checkout_created` / `payment_created` are not payment or revenue
  - confirmed/received do not auto-onboard or infer WON; onboarding needs an explicit event
  - raw `CHARGEBACK` opens `payment_chargeback`, never auto-WON/LOST
- On first `payment_received` for `CFG-DIAG-EXP-v1`: reminder exception `counsel_review_due` (target 10 business days). Not a kill switch. Does not block cash or onboarding.
- On `payment_confirmed`: finance exception `nfse_manual_queue` (manual NFS-e queue). Does not auto-issue. Does not change price. Does not hold the commercial chain closed.
- Chargeback is no longer remapped only to `payment_refund`. Existing code `payment_chargeback` is emitted.

## Unchanged / already shipped

- `checkout_created` / `payment_created` ≠ revenue
- `payment_confirmed` increments `ConfirmedCount` only
- `payment_received` is the only `ReceivedCents` increment
- synthetics stay off the real scoreboard (`include_synthetic=0`)
- `CONFENGE_AUTO_SEND_ENABLED` remains **false**. Isolated env cannot reactivate auto-send.
- WON/LOST still require human or document evidence
- No second billing ledger. No SmartLic / extra-cli edits.

## Deploy posture

- Consumer can be deployed as a **listener** on `confenge.commercial_event.v1`.
- Provider adapter default remains sandbox/disabled. No production Asaas key is assumed.
- No public real-money mutation. No live checkout, charge, refund, or NFS-e issuance from this SHA.
- `LIVE_PROVEN` is **not** claimed.

## Classification

| Label | Status |
| --- | --- |
| CODE_PROVEN | yes (intel tests) |
| CI_PROVEN | pending remote CI |
| MERGED | no |
| DEPLOYED | no |
| LIVE_PROVEN | no |
| SANDBOX | yes |
| REAL_EXTERNAL | no |
| auto-send | false |
