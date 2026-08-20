# EVIDENCE — CONFENGE-WARMBLY-COMMERCIAL-EVENT-PRODUCTION-CANARY-01

## Verdict

`COMMERCIAL_EVENT_CONSUMER_LIVE_WAITING_PRODUCER_CANARY`

This run re-read the live Netcup SHA and re-ran the HMAC canary. PR #101 remains merged. HMAC ingest of `confenge.commercial_event.v1` is proven on the real transport with labeled SYNTHETIC bodies. The web-cfg producer has not announced a production POST of this envelope (`CONFENGE_COMMERCIAL_EVENT_ENABLED` default off). Issue #47 stays OPEN. Residual: `FIRST_REAL_CONSENTED_COMMERCIAL_EVENT`.

## Disposition of PR #101

- Already squash-merged as `93dd039d7b9b310458beff8a6bd8819a61da6399` on 2026-08-20T15:47:05Z (no clone PR)
- origin/main at this run: `08b5ec06522d55f282a46529f49ce9f42805b6d7` (docs-only successor of the consumer SHA)
- Local gating on origin/main: `gofmt -l` empty on intel + inbound handler; `go test ./internal/app/confenge/intel/ -count=1` pass; `go test ./internal/api/handler/ -count=1 -run 'TestConfengeInboundWebhookCommercialEventHMAC|TestConfengeInboundHealth|TestConfengeInboundWebhookInvalidHMAC'` pass; `make lint` pass
- `CONFENGE_AUTO_SEND_ENABLED` stayed false. No closing keywords against #47

## Deploy (re-read this run, no redeploy)

Consumer already live. Running SHA contains the consumer, so this run did not rebuild or flip flags.

| Item | Value |
| --- | --- |
| host | `v2202607385716487230` `/opt/warmbly-confenge` |
| deployed SHA | `93dd039d7b9b310458beff8a6bd8819a61da6399` |
| git dirty | 0 |
| origin/main consumer | same SHA; docs successor `08b5ec06` |
| backend image | `sha256:59997cf92a66079d01053c9e87eeffa2e69dfb287522bcf2a8aa2643fec207b6` |
| schema_migrations | `106` dirty `f` |
| inbound health (loopback, twice) | READY, `auto_send_enabled=false`, `dispatch_attempted=false` |
| inbound health (public, twice) | same |
| accepted_event_versions | `confenge.commercial_event.v1`, `confenge.search_observation.v1` |
| `/live` `/ready` `/health/deps` | HTTP 200, ready=true, planes ok |
| flags | auto-send false, GREEN false, WhatsApp false, human approval true |
| kill switch | present / dispatch paused |

## HMAC canary (loopback, twice) stamp `20260820T2135Z`

Transport: `POST http://127.0.0.1:8080/api/v1/webhooks/confenge/inbound` with `X-Warmbly-Signature`. No SQL insert.

| Check | Round 1 | Round 2 |
| --- | --- | --- |
| invalid `t=1,v1=deadbeef` | HTTP 401 | HTTP 401 |
| valid SYNTHETIC `offer_selected` | HTTP 201 | HTTP 201 |
| replay same `event_id` | HTTP 200 `replay=true` | HTTP 200 `replay=true` |
| unknown type `TELEPORTED` | HTTP 201 held, canonical=UNKNOWN | HTTP 201 held, canonical=UNKNOWN |

Default INBOUND NOW: count 0, commercial canary leads 0.

## Clean SYNTHETIC sequence

`external_reference=SYNTHETIC-CE-SEQ-20260820T2135Z`

`offer_selected` → `eligibility_submitted` → `capacity_approved` → `terms_accepted` → `checkout_created` → `payment_pending` → `payment_received`

Durable chain after refund (Postgres, no PII):

- `synthetic=true`
- `offer_id=CFG-DIAG-EXP-v1`
- `payment_received` HTTP 201 `onboarding=ONBOARDING_ELIGIBLE` `received_count=1`
- replay HTTP 200 `replay=true` `received_count=1` (not doubled)
- `payment_refunded` HTTP 201 `canonical=refunded` `onboarding=ONBOARDING_BLOCKED` `received_amount_cents=800000` `refunded_amount_cents=800000` `received_count=1` (history kept)

`checkout_created` and `payment_pending` stayed `ONBOARDING_BLOCKED` with no received cents.

## Invariants on the live POST path

- capacity rejected held; checkout after reject held with `no_capacity`
- out-of-order `payment_received` held with `out_of_order`
- Extra `CFG-EXTRA-HIST-10K` held with `private_extra_as_offer`; chain `offer_id` null; `include_synthetic=0` does not list Extra
- `subscription_active` on `CFG-DIRB2G-180-v1` stayed `ONBOARDING_BLOCKED` with no received cents
- callback/`subscription_active` with `received_revenue=true` did not become `payment_received`; onboarding blocked
- store-unavailable 503 proven by shipped `JoinUnavailable` mapping + tests; production Postgres was not taken down

## Executive

`include_synthetic=0`: `CFG-DIAG-EXP-v1` absent. Commercial received revenue 0. Qualified pipeline 0. Extra absent. No SLA field. No #55 baseline.

`include_synthetic=1`: `by_offer_version` for `CFG-DIAG-EXP-v1` shows selected, checkout, payment_received, onboarding_eligible, refund, exceptions, qualified_pipeline, received_revenue_cents, denominator_chains, and #55 stage timestamps (`eligibility_submitted_at`, `capacity_decision_at`, `terms_accepted_at`, `checkout_created_at`, `payment_received_at`). `causal_proof=false`. UNKNOWN visible. Denominators present. Extra appears only as an exception-only synthetic row (selected=0, revenue=0), not as a public catalog offer.

## Producer

web-cfg `scripts/offers/events.cjs` is the contract factory. `netlify/functions/lib/commercial-event.cjs` is persist-first HMAC outbox, gated by `CONFENGE_COMMERCIAL_EVENT_ENABLED` (default off). Production handoff still posts `confenge.inbound.v1` leads. Exact producer command: [PRODUCER-CANARY-COMMAND.txt](./PRODUCER-CANARY-COMMAND.txt).

Producer capability probe currently reads `capabilities` / `accepted_versions` / `versions`. Warmbly health publishes `accepted_event_versions`. That mismatch is a producer-side contract note; this campaign did not edit web-cfg and did not redeploy to add aliases.

## Real event search

`outreach_intel_event_receipts`: 52 rows, all SYNTHETIC ids. Zero non-synthetic provider/event ids. No real consented external commercial event was ingested. #47 stays OPEN.

## Security notes

HMAC verified before ingest. Invalid secret 401. Query PII rejected by shipped tests. Commercial envelope is not a lead. Auto-send stayed false. Live 503 was not forced by taking Postgres down.
