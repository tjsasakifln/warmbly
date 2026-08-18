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

`RegisterHumanOutcome` keys each capture as `human:<slot>:<action>:<lead|unbound>`. A slot-only `envelope:<SLOT>` from the dashboard is expanded, so EXTRA attempted then EXTRA reached both persist. Replay of the same (slot, action, lead) returns the first event. `follow_up_at` is copied onto the commercial event and chain; `follow_up` without a date is a held exception.

WON / LOST / receita without evidence are held exceptions. They are not recorded as TRUE on the scoreboard.
