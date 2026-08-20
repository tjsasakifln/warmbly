# EVIDENCE — CONFENGE-WARMBLY-COMMERCIAL-EVENT-PRODUCTION-CANARY-01

## Verdict

`COMMERCIAL_EVENT_CONSUMER_LIVE_WAITING_PRODUCER_CANARY`

PR #101 is merged and live on Netcup. HMAC ingest of `confenge.commercial_event.v1` is proven on the real transport with labeled SYNTHETIC bodies. The web-cfg producer has not announced a production POST of this envelope. Issue #47 stays OPEN. Residual: `FIRST_REAL_CONSENTED_COMMERCIAL_EVENT`.

## Disposition of PR #101

- Rebased onto `origin/main` `2a8fa69d` (already up to date, no production-convergence conflicts)
- Local gating: `gofmt -l` empty, `make lint` pass, `go test ./internal/app/confenge/intel/ -count=1` pass, `go test ./internal/api/handler/ -count=1 -run TestConfengeInbound` pass, `web` typecheck pass, `docs` types:check pass
- Squash-merged as `93dd039d7b9b310458beff8a6bd8819a61da6399` (no clone PR)
- #47 closing keywords avoided in the PR body and squash message; issue remained open

## Deploy

| Item | Value |
| --- | --- |
| previous SHA | `ab37782632922f6f150756c9686f0869df4add19` |
| deployed SHA | `93dd039d7b9b310458beff8a6bd8819a61da6399` |
| origin/main | same |
| backend image | `sha256:59997cf92a66079d01053c9e87eeffa2e69dfb287522bcf2a8aa2643fec207b6` |
| profile | `GO_TAGS=minprofile` |
| schema_migrations | `106` dirty `f` (no new migration in #101) |
| backup | `/opt/warmbly-confenge/data/backups/confenge-vps/warmbly-confenge-20260820T154831Z.tar.gz` |
| inbound health | READY, `auto_send_enabled=false`, `dispatch_attempted=false` |
| accepted_event_versions | `confenge.commercial_event.v1`, `confenge.search_observation.v1` |
| flags | auto-send false, GREEN false, WhatsApp false, human approval true |
| kill switch | paused / `reason=deploy_preflight` |

## HMAC canary (loopback, twice)

Invalid `t=1,v1=deadbeef` → HTTP 401 both rounds.

Valid SYNTHETIC `offer_selected` → HTTP 201 then replay HTTP 200 `replay=true`.

Unknown type `TELEPORTED` → HTTP 201, held, `canonical=UNKNOWN`, raw preserved.

No commercial event created an INBOUND NOW lead (`include_synthetic=0` count 0).

## Clean SYNTHETIC sequence (fresh `external_reference`)

`offer_selected` → `eligibility_submitted` → `capacity_approved` → `terms_accepted` → `checkout_created` → `payment_pending` → `payment_received`

| Step | HTTP | onboarding | received_amount_cents |
| --- | --- | --- | --- |
| offer_selected | 201 | ONBOARDING_BLOCKED | empty |
| checkout_created | 201 | ONBOARDING_BLOCKED | empty |
| payment_pending | 201 | ONBOARDING_BLOCKED | empty |
| payment_received | 201 | ONBOARDING_ELIGIBLE | 800000 |
| replay payment_received | 200 | ONBOARDING_ELIGIBLE | 800000 (not doubled) |
| payment_refunded | 201 | ONBOARDING_BLOCKED | 800000 (history kept, status refunded) |

Capacity rejected holds. Checkout after reject is held with `no_capacity`. Out-of-order `payment_received` is held with `out_of_order`.

## Executive

`include_synthetic=0`: `CFG-DIAG-EXP-v1` absent. Qualified pipeline 0. No SLA. No #55 baseline.

`include_synthetic=1`: `by_offer_version` for `CFG-DIAG-EXP-v1` shows selected, checkout, payment_received, onboarding_eligible, refund, exceptions, qualified_pipeline, received_revenue_cents=800000, denominator_chains, and #55 stage timestamps (`eligibility_submitted_at`, `capacity_decision_at`, `terms_accepted_at`, `checkout_created_at`, `payment_received_at`). `causal_proof=false`.

## Security notes

HMAC verified before ingest. Invalid secret 401. Query PII rejected. Commercial envelope is not a lead. Auto-send stayed false. Live 503 was not forced by taking Postgres down; shipped `JoinUnavailable` maps to 503.

## Producer

web-cfg `scripts/offers/events.cjs` is the contract factory. Production handoff still posts `confenge.inbound.v1` leads. Exact producer command: [PRODUCER-CANARY-COMMAND.txt](./PRODUCER-CANARY-COMMAND.txt).
