# Idempotency and replay

Shipped units: `IngestInboundLead`, `POST /api/v1/webhooks/confenge/inbound`, `IngestEvent`, `RegisterHumanOutcome`.

## Inbound persist-first

1. First signed body with `lead_id=webcfg-truth-1` persists a durable receipt. `duplicate=false`, `dispatch_attempted=false`.
2. Byte-equivalent replay of the same body returns the first receipt. `duplicate=true`. No second commercial action.
3. Invalid HMAC (`wrong-secret` or `t=1,v1=deadbeef`) is HTTP 401. Nothing persists.

Covered by `TestInboundTruthScoreboardShippedPath` and `TestConfengeInboundWebhookInvalidHMACIs401`. `-count=2` consistent.

## Commercial event

Same `event_id` or `idempotency_key` is a replay (`JoinResult.Replay=true`, `Created=false`). Unknown types, out-of-order outcomes, and a nil store land on the exception queue with `owner`, `reason`, and `next_action`.

## Human outcome

`RegisterHumanOutcome` uses `idempotency_key`. Empty EXTRA / ACCOUNT_1 / ACCOUNT_2 / ACCOUNT_3 envelopes reuse `envelope:<SLOT>`. Replay of EXTRA attempted does not invent IDs or open a second chain.

WON / LOST / receita without evidence are held exceptions. They are not recorded as TRUE on the scoreboard.
