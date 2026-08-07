# Netcup deployment notes (CONFENGE Warmbly)

Deploy Warmbly as an **isolated Compose project** next to extra-cli. Do not share
Postgres, Redis, or Kafka with the intelligence plane.

## Preflight checklist

- Inventory ports, volumes, reverse proxy, DNS, TLS, disk, RAM, and extra-cli
  services already running on the VPS.
- Choose non-colliding names: project `warmbly-confenge`, network
  `warmbly_confenge_net`, volumes `warmbly_confenge_pg` / `_redis`.
- Do not mount the extra-cli datalake volume into Warmbly.
- Do not grant Warmbly credentials to the extra-cli database.

## Integration path

```text
extra-cli export feed (HTTPS or file drop)
        → Warmbly CONFENGE_EXTRA_CLI_FEED_URL
Warmbly outcome outbox
        → CONFENGE_OUTCOME_WEBHOOK_URL (extra-cli receiver)
```

## Required env (minimum)

```env
CONFENGE_OUTREACH_ENABLED=true
CONFENGE_AUTO_SEND_ENABLED=false
CONFENGE_REQUIRE_HUMAN_APPROVAL=true
CONFENGE_EXTRA_CLI_FEED_URL=https://...
CONFENGE_EXTRA_CLI_ALLOWED_HOSTS=...
CONFENGE_OUTCOME_WEBHOOK_URL=https://...
CONFENGE_OUTCOME_WEBHOOK_SECRET=...
BOX_OUTLOOK_CLIENT_ID=...
BOX_OUTLOOK_CLIENT_SECRET=...
```

Use Microsoft Graph OAuth already supported by Warmbly. Do not put mailbox
passwords in `.env`.

## Ops

| Action | Notes |
| --- | --- |
| Deploy | Isolated compose up; migrations apply on backend boot |
| Smoke | `/health`, import fixture dry-run, generate template draft |
| Backup | Postgres volume only for Warmbly project |
| Restore | Restore volume, restart backend, verify migration version |
| Rollback | Set `CONFENGE_OUTREACH_ENABLED=false`; optional down migrations |
| Upgrade | Pull image/tag, recreate containers, watch migrations |

## External access

If DNS/TLS is not ready, use SSH tunnel to the dashboard/API. Do not expose an
unauthenticated panel.

## Proof bar

Do not declare production ready without:

1. Clean import of synthetic feed
2. Review + approve path
3. Mailbox Graph connection test to an operator-owned address
4. Outcome outbox delivery or documented export path
5. Backup/restore drill
