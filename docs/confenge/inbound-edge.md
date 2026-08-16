# Public inbound edge (web-cfg handoff)

Secret-free contract for Netlify / web-cfg. No HMAC secret is published
here. The shared secret stays in the Warmbly process env and the matching
Netlify env `CONFENGE_INBOUND_WEBHOOK_SECRET`.

## Canonical URL

| Field | Value |
| --- | --- |
| Canonical URL | `https://api.confenge.com.br/api/v1/webhooks/confenge/inbound` |
| Host | `api.confenge.com.br` |
| Path | `/api/v1/webhooks/confenge/inbound` |
| Health | `GET https://api.confenge.com.br/api/v1/webhooks/confenge/inbound/health` |
| Method | `POST` only on the inbound path. `GET`/`HEAD` only on `/health`. |

Set on Netlify:

```text
CONFENGE_INBOUND_WEBHOOK_URL=https://api.confenge.com.br/api/v1/webhooks/confenge/inbound
```

Do not retarget `OPS_WEBHOOK_URL`. That is the Slack-style `confenge.lead`
HMAC, a different contract.

## Expected health

HTTP 200 JSON, no secret, no dest org id, no PII:

```json
{"status":"READY","auto_send_enabled":false,"dispatch_attempted":false,"reasons":[]}
```

A TCP/TLS timeout is UNREACHABLE. That is distinct from POST `401` (HMAC)
and POST `5xx` (persist / dest store). A `200` with `status=BLOCKED` means
the process is up but receive is not configured (secret, dest org, or
auto-send). `auto_send_enabled` must stay `false`.

## HMAC

Version `v1` only.

```text
X-Warmbly-Signature: t=<unix>,v1=<hex(hmac_sha256(secret, "<unix>." + body))>
```

`X-Confenge-Signature` is accepted as an alias. Clock skew is 5 minutes.
`Idempotency-Key` is optional; the durable key is `lead_id` / `receipt_id`.
Replay of the same lead returns HTTP 200 and does not create a second
commercial action.

## Allowed host / origin policy

- TLS SNI and `Host` must be `api.confenge.com.br`.
- Other names on :443 are closed (`444`) and are not proxied.
- This edge does not require a browser `Origin`. web-cfg is server to server.
- Only two application paths are proxied to the Warmbly loopback listener:
  `GET /api/v1/webhooks/confenge/inbound/health` and
  `POST /api/v1/webhooks/confenge/inbound`.
- Every other path, including `/confenge`, `/admin`, `/v1`, operator UI,
  and the rest of the SaaS API, returns 404. This CONFENGE operator-mode
  host is not the multi-tenant public API.
- PII is not accepted on the query string. Send the body.

## Timeout and body semantics

| Limit | Value |
| --- | --- |
| Edge connect | 5s |
| Edge send / read | 30s |
| Client body / header | 15s |
| Request body | 1 MiB (matches the handler) |
| Health caller budget | 10s recommended; treat timeout as UNREACHABLE |
| HMAC skew | 5 minutes |
| Rate limit (POST) | 20 req/min/IP, burst 10, HTTP 429 |
| Rate limit (GET health) | 30 req/min/IP, burst 20, HTTP 429 |

Unsigned POST or `X-Warmbly-Signature: t=1,v1=deadbeef` → 401.
Invalid JSON / missing `lead_id` and `receipt_id` (after a valid HMAC) → 400.
Query `?email=` → 400.
Dest org or store unavailable → 503 / 500, never 2xx.

## Production SHA

Warmbly process on this host: `deabc11e715d508a68ee231376148b52ee4b8aca`.

`CONFENGE_AUTO_SEND_ENABLED=false` is required. Inbound never dispatches.

Public proof on 2026-08-16: DNS A `159.195.18.88` (TTL 300, DNS only), valid
Let's Encrypt leaf/SAN for `api.confenge.com.br`, external health HTTP 200
`READY` with `auto_send_enabled=false`, and external unsigned/invalid-HMAC
POSTs rejected with 401. The backend listener remains `127.0.0.1:8080`.

## DNS (operator)

Zone `confenge.com.br` is on Cloudflare (`grannbo.ns.cloudflare.com`,
`kai.ns.cloudflare.com`). Canonical record, DNS-only (grey cloud):

```text
api.confenge.com.br.  300  IN  A  159.195.18.88
```

Keep this name DNS only. The VPS nginx presents the Let's Encrypt leaf for
`api.confenge.com.br` directly.

## Install on the VPS

```bash
# as root, repo at /opt/warmbly-confenge
deploy/confenge-vps/inbound-edge-install.sh
```

Backend stays on `127.0.0.1:8080`. Postgres stays on `127.0.0.1:15432`.
SSH stays on `2222`. extra-cli feed stays on `:8443`.
