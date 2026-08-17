# CONFENGE-PRODUCTION-CLOSEOUT-01 — Warmbly baseline

Approval: `OWNER_CONDITIONAL_PREAPPROVAL_CONFENGE_PRODUCTION_CLOSEOUT_01`

## Git / deploy

| Field | Value |
|---|---|
| origin/main | `6612b7ed3769bd8bf0341ed64fb4b638ccd7bf09` |
| VPS checkout `/opt/warmbly-confenge` | `d6eb0ef1a` (#82 fail-close auto-send) |
| Drift | main is **ahead** of deployed SHA (live/ready/health/deps, minprofile, exception queue, fork-drift) |
| Open PRs | none |

## Live inbound (public)

`GET https://api.confenge.com.br/api/v1/webhooks/confenge/inbound/health` ×2:

```json
{"auto_send_enabled":false,"dispatch_attempted":false,"reasons":[],"status":"READY"}
```

Unsigned POST → HTTP 401 `invalid inbound signature`.

`/live` `/ready` `/health/deps` are **not** on the public nginx allowlist (by design). Localhost `:8080` on deployed SHA: `/health=200 {"status":"ok"}`; `/live` `/ready` `/health/deps` 404 (those routes landed after `d6eb0ef1`).

## Flags (host `.env`, not secrets)

- `CONFENGE_AUTO_SEND_ENABLED=false`
- `CONFENGE_GREEN_AUTORUN_ENABLED=false`
- `CONFENGE_DISPATCH_PAUSED=true`
- `CONFENGE_SENDING_PAUSED=true`
- `CONFENGE_REQUIRE_HUMAN_APPROVAL=true`
- `CONFENGE_WHATSAPP_ENABLED=false`

HMAC: `CONFENGE_INBOUND_WEBHOOK_SECRET` PRESENTE in `deploy/confenge-vps/.env` and in running container. AUSENTE from `/opt/warmbly-confenge/.env`.

## Next

Deploy `origin/main` `6612b7ed` with existing compose overlay, preserve vps `.env`, keep auto-send off, then re-probe inbound + localhost `/live` `/ready` `/health/deps`.
