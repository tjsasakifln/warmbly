# CONFENGE architecture split (canonical)

Warmbly is the **execution plane**. extra-cli is the **intelligence plane**.
They do not share a host in the go-live topology.

```text
┌──────────────────────────── VPS (Netcup) ────────────────────────────┐
│  extra-cli + datalake                                                 │
│  enrichment / activation / EMAIL_SEND_READY supply                    │
│  confenge.outreach.v1 feed drop                                       │
│  HTTPS :8443  → static feed + /webhooks/warmbly/outcome (HMAC)        │
│  NO Warmbly runtime · NO mailbox passwords · NO Graph/M365            │
└───────────────────────────────────┬──────────────────────────────────┘
                                    │ pull feed / push outcomes
┌───────────────────────────────────▼──────────────────────────────────┐
│  Operator WSL / Windows (while machine is on)                         │
│  Warmbly (make confenge-local / backend+worker+web)                   │
│  Hostinger SMTP:587 + IMAP:993 for tiago.sasaki@confenge.com.br       │
│  Governor, GREEN policy path, Unibox, CRM                             │
└──────────────────────────────────────────────────────────────────────┘
```

## Email channel (factual)

| Item | Value |
| --- | --- |
| Address | `tiago.sasaki@confenge.com.br` |
| Host | **Hostinger** (not Exchange Online / M365) |
| SMTP | `smtp.hostinger.com:587` STARTTLS |
| IMAP | `imap.hostinger.com:993` SSL |
| Graph / Azure app | **Not required** for CONFENGE go-live |
| Mailpit | Local tests only |

## Operator loop (minimal)

1. VPS: extra-cli expands EMAIL_SEND_READY and refreshes feed.
2. Laptop session start:
   - `scripts/confenge_pull_feed_from_vps.sh`
   - `make confenge-local` (use `CONFENGE_API_HOST=127.0.0.1:18080` if :8080 is taken)
   - `scripts/confenge_hostinger_connect.sh` (needs `CONFENGE_MAILBOX_PASSWORD`)
   - `scripts/confenge_self_smoke.sh` before any lead send
3. Outcomes → VPS via `CONFENGE_OUTCOME_WEBHOOK_URL`.

## Local env keys

| Variable | Notes |
| --- | --- |
| `CONFENGE_EXTRA_CLI_FEED_URL` | `file://…/email_send_ready_feed.json` after pull |
| `CONFENGE_OUTCOME_WEBHOOK_URL` | `https://159.195.18.88:8443/webhooks/warmbly/outcome` |
| `CONFENGE_OUTCOME_WEBHOOK_SECRET` | VPS `/opt/confenge-plane/outcome.secret` |
| `CONFENGE_MAILBOX_EMAIL` | `tiago.sasaki@confenge.com.br` |
| `CONFENGE_SMTP_HOST/PORT` | `smtp.hostinger.com` / `587` |
| `CONFENGE_IMAP_HOST/PORT` | `imap.hostinger.com` / `993` |
| `CONFENGE_MAILBOX_PASSWORD` | local only; never VPS; never git |

## Public exposure

| Exposure | Required? |
| --- | --- |
| VPS :8443 feed + outcome webhook | Yes (or SSH tunnel) |
| VPS Warmbly UI | No |
| Mailbox password on VPS | **Forbidden** |

## Kill switch (laptop)

```bash
make confenge-stop-sending
make confenge-resume-sending
```
