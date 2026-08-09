# Netcup ops notes (CONFENGE intelligence plane only)

Canonical topology: the VPS runs **extra-cli + feed + outcome receptor**.
Warmbly execution (Hostinger SMTP/IMAP on the operator laptop) runs on
**WSL/Windows**, not on the VPS. No Graph/M365 secrets on either side for CONFENGE.

See [architecture-split.md](./architecture-split.md).

## What belongs on the VPS

- extra-cli / datalake
- continuous enrichment and EMAIL_SEND_READY supply
- `confenge.outreach.v1` feed generation under `/var/lib/extra-consultoria/warmbly-feed`
- HTTPS feed front (`/opt/confenge-plane`, port **8443**)
- HMAC outcome receptor (`serve-outcomes` on loopback **8790**, proxied at
  `https://<vps>:8443/webhooks/warmbly/outcome`)

Do **not** store mailbox passwords, Hostinger credentials, or any OAuth secrets on the VPS.
The production CONFENGE mailbox is **Hostinger SMTP/IMAP** on the operator laptop, not Microsoft 365.

## Integration path

```text
extra-cli (VPS) → HTTPS feed :8443
        → local Warmbly CONFENGE_EXTRA_CLI_FEED_URL (file pull or HTTPS)
local Warmbly outcome outbox
        → CONFENGE_OUTCOME_WEBHOOK_URL=https://<vps>:8443/webhooks/warmbly/outcome
```

## Local Warmbly env (laptop)

```env
CONFENGE_OUTREACH_ENABLED=true
CONFENGE_AUTO_SEND_ENABLED=false
CONFENGE_REQUIRE_HUMAN_APPROVAL=true
CONFENGE_GREEN_AUTORUN_ENABLED=true
# after: scripts/confenge_pull_feed_from_vps.sh
CONFENGE_EXTRA_CLI_FEED_URL=file:///…/data/confenge-plane/email_send_ready_feed.json
CONFENGE_OUTCOME_WEBHOOK_URL=https://159.195.18.88:8443/webhooks/warmbly/outcome
CONFENGE_OUTCOME_WEBHOOK_SECRET=<from VPS /opt/confenge-plane/outcome.secret>
# LOCAL ONLY — Hostinger (not Graph/M365):
CONFENGE_MAILBOX_EMAIL=tiago.sasaki@confenge.com.br
CONFENGE_SMTP_HOST=smtp.hostinger.com
CONFENGE_SMTP_PORT=587
CONFENGE_IMAP_HOST=imap.hostinger.com
CONFENGE_IMAP_PORT=993
CONFENGE_MAILBOX_PASSWORD=...
```

Connect + smoke (laptop, after stack is up):

```bash
scripts/confenge_hostinger_connect.sh
scripts/confenge_self_smoke.sh   # self-send only; no leads
```

## Public exposure

| Open on VPS | Purpose |
| --- | --- |
| TCP 8443 | Feed download + outcome webhook for the laptop |
| TCP 2222 | SSH ops |

Legacy `warmbly-confenge` compose on the VPS may remain stopped with volumes
intact for recovery; it is not the execution plane.

## Proof bar

1. VPS feed serves current EMAIL_SEND_READY stock
2. Local Warmbly imports feed (file or HTTPS)
3. Hostinger SMTP/IMAP self-send/reply/bounce on operator-owned address only
   (`scripts/confenge_self_smoke.sh`; no Azure / Graph)
4. Outcome HMAC delivery to VPS receptor
5. Kill switch on local Warmbly (`make confenge-stop-sending`)
