# Evidence — CONFENGE-WARMBLY-FIRST-REAL-ACTION-01

Redacted. No phone, email, person name, CNPJ, or secret values.

## Verdict

Technical DONE: `FOUNDER_ACTION_REQUIRED_SINGLE_CALL`.

Commercial DONE of GitHub #42 is blocked on the founder placing the single ROUTED_CALL and reporting the observed result. Software does not telephone and does not invent PSTN outcomes. A prior `ATTEMPTED` row is not an observed call.

Auto-send stays off. GREEN autorun stays off. WhatsApp auto-send stays off. Dispatch remains paused.

## Runtime reconcile

| Item | Value |
| --- | --- |
| origin/main | `dd4490825f01350add510f41746500947d83f850` |
| snapshot SHA | `dd4490825f01350add510f41746500947d83f850` |
| pre-promote deployed SHA | `6612b7ed3769bd8bf0341ed64fb4b638ccd7bf09` |
| post-promote deployed SHA | `dd4490825f01350add510f41746500947d83f850` (matches origin/main) |
| backend image | `sha256:85514c9687859a9c810ca6604e3dc7d6e1068a69c40d89a500cf4f04269ff2a2` |
| promoted_at | 2026-08-19T00:49:51Z |
| rollback backup | `/opt/warmbly-confenge/data/backups/confenge-vps/warmbly-confenge-20260819T004638Z.tar.gz` |
| profile | `GO_TAGS=minprofile` (postgres, redis, nats, local KMS, filesystem blobs) |
| `/health` | 200 `{"status":"ok"}` |
| `/live` | 200 `{"live":true,"status":"live"}` |
| `/ready` | 200 required planes ok (control_plane, db, cache, queue, event_processing, worker_heartbeat, provider_edge) |
| `/health/deps` | 200 same |
| `/opsprobe` | 404 (not shipped) |
| inbound `/health` public and loopback | 200 READY, `auto_send_enabled=false`, `dispatch_attempted=false` |
| `GET /v1/confenge/status` | `kill_switch=true`, `sending_allowed=false`, `auto_send_enabled=false`, `require_human_approval=true` |
| dispatch | PAUSED (`paused` / `reason=deploy_preflight`; API pause_reason `warmbly002_post_deploy`) |
| GREEN autorun | OFF |
| WhatsApp | OFF |
| `CONFENGE_SENDING_PAUSED` | true |
| extra feed | STALE (last success 2026-08-13). Does not block the manual call. |

## Secret classification

`CONFENGE_INBOUND_WEBHOOK_SECRET` is present in the authorized VPS env (length 64). Not printed. Not `BLOCKED_SECRET_FOR_SIGNED_CANARY`.

## HMAC canary (labeled SYNTHETIC, excluded from the real scoreboard)

Live loopback `POST /api/v1/webhooks/confenge/inbound` plus public edge:

| Step | Result |
| --- | --- |
| unsigned | HTTP 401 `invalid inbound signature` |
| `t=1,v1=deadbeef` | HTTP 401 |
| signed SYNTHETIC DNC | HTTP 201, `next_action=SUPPRESSED`, `dispatch_attempted=false`, `status=SUPPRESSED` |
| replay same body/sig | HTTP 200 `duplicate=true`, same row id |
| public unsigned | HTTP 401 |
| INBOUND NOW | still empty; canary lead id absent |
| executive `include_synthetic=false` | inbound_qualified_pipeline 0→0; qco/conversations/meetings/won unchanged; pipeline 47→47 |

Canary lead_id: `synthetic-first-real-action-01-20260819T004552Z`. Row id: `4d6cf347-8729-4257-a12a-411b0dcbf4b3`.

## Outcome registrar (shipped tests on origin/main)

```
go test ./internal/app/confenge/ -run 'TestInboundShippedPathIngestToOutcome|TestReachabilityContractualFixtures|TestInboundHTTPContractHMACAndNoSend|TestCommercialOutcomeFollowupAndGuards|TestInboundNowSkips'
go test ./internal/api/handler/ -run 'TestConfengeInboundWebhookRejectsQueryPIIAndAcceptsSignedBody'
go test ./internal/app/confenge/intel/ -run 'TestEmptyEnvelopes|TestRegisterHumanOutcome|TestScoreboard'
```

All PASS on `dd449082`. `TestLabeledCanaryProvesPathAndExcludesExecutive` is not on origin/main; live SYNTHETIC canary plus skip tests cover exclusion.

Live registrar after promote (`POST /v1/confenge/intel/human-outcomes`): SYNTHETIC `attempted` HTTP 201 identity `lead:synthetic-human-first-real-20260819T005034Z`; replay HTTP 200 same identity/metric_key; INBOUND NOW still empty; executive inbound/qco/won unchanged; scoreboard `include_synthetic=false`, `auto_send_enabled=false`. Seven stages stay independent (`qualified_conversation` 0/47, proposal/revenue UNKNOWN).

CLI signature (no token = template only, no POST):

```
scripts/confenge_human_outcome.sh <action> [lead_id] [envelope]
```

Real commercial path already on the running API:

```
POST /v1/confenge/actions/:id/outcome
```

Mapped verbs: attempted=`ATTEMPTED`, routed=`CONTACTED`, reached=`TARGET_REACHED`, not_reached=`NO_ANSWER`, wrong_route=`INVALID_ROUTE`/`WRONG_PERSON`, follow_up=`FOLLOW_UP`. WON/LOST/revenue unused.

## Selected action (redacted)

Exactly one card. INBOUND NOW was empty (`{"data":[]}`). Selection is the first imported DUI `ROUTED_CALL` already on Today.

- action_id: `0d8c8cd0-3339-5224-b5c0-6a2160b6ca56`
- account_id: `2ca3423b-874f-4725-b7a3-611491605115`
- action_type: `ROUTED_CALL`
- lane: `ROUTED_CALL_QUEUE`
- reachability: `R3_ROUTED_TO_NAMED_PERSON` / `ROUTES_TO_NAMED_PERSON`
- why-now code: `OPERATOR_PROJECTION`
- state: `IN_PROGRESS` with last_outcome `ATTEMPTED` (not an observed PSTN result)
- exclusions: not DNC, not blocked, not third-party, `email_sendable=false`, `dispatchable=false`
- freshness: `TARGET_FIT_FRESH`

Private card: `FOUNDER_ACTION_REQUIRED_CALL.txt` outside git, mode 0600. Agent does not call.

## Learning (pre-human)

- action: ROUTED_CALL card prepared; timestamp in STATE.json
- route class/relation: R3 / ROUTES_TO_NAMED_PERSON
- observed outcome: UNKNOWN (human has not executed)
- next action: founder places the call in 09:00–18:00 America/Sao_Paulo, then records the observed code
- repeat | change | stop: wait for observed result; default repeat same route if no answer; do not change identity; stop email
- candidate correction: none until PSTN is observed
- exception owner: founder for the untransferable call; inbound-ops for stale extra feed (non-blocking)

Seven executive stages stay independent. This card does not increment inbound qualified pipeline, proposal, or revenue.

## Issues

- #42 remains OPEN until the founder reports an observed result.
- #47 remains residual consumer only. No billing ledger work in this campaign.
