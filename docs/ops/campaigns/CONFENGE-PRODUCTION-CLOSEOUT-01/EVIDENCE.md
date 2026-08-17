# Evidence — Warmbly

## Public inbound health ×2 — LIVE_PROVEN

- 2026-08-17T12:32:08Z request_id `76e5711b-9159-4a6c-afe7-f55e88e701a3` HTTP 200
- 2026-08-17T12:32:08Z request_id `102bbdd7-ca37-405b-8b51-93086f5b376a` HTTP 200
- Body both: `{"auto_send_enabled":false,"dispatch_attempted":false,"reasons":[],"status":"READY"}`

## HMAC reject unsigned — LIVE_PROVEN

POST unsigned → 401 `invalid inbound signature` request_id `4e8d9d30-fb06-48b5-aed2-bf4c5654d74d`

## Deployed SHA — LIVE_PROVEN

`/opt/warmbly-confenge` rebuilt from `6612b7ed` at 2026-08-17T12:45:12Z (`GO_TAGS=minprofile`).

## Local health after rebuild — LIVE_PROVEN

- `/live` 200 `{"live":true,"status":"live"}`
- `/ready` 200 all required planes `ok` (control_plane, db, cache, queue, event_processing, worker_heartbeat, provider_edge)
- `/health/deps` 200 same
- inbound/health READY, auto_send false

## CONTROLLED_OWNER_CANARY — LIVE_PROVEN

- First POST HMAC valid → HTTP 201, `lead_id=synthetic-owner-canary-closeout-83431301ee10`, `next_action=SUPPRESSED`, `dispatch_attempted=false`
- Replay same lead/receipt → HTTP 200 `duplicate=true`, same row id `97489e04-2c13-4666-bd6e-19a9849f2ec9`
- Invalid HMAC `t=1,v1=deadbeef` → HTTP 401
- Does **not** close #47, web-cfg #60, or #88

