# CONFENGE VPS execution plane

Topology target:

```text
extra-cli + datalake (VPS)
        │ confenge.outreach.v1
        ▼
Warmbly warmbly-confenge (same VPS, isolated Docker project)
        │ Hostinger SMTP/IMAP client
        │ human approval UI (browser via SSH tunnel)
        ▼
confenge.outcome.v1 → extra-cli receptor
```

Local laptop = browser (and optional SSH) only. No local Docker/WSL stack required for daily send/sync.

## What this pack is

Directory: `deploy/confenge-vps/`

| Script | Purpose |
| --- | --- |
| `install.sh` / `gen-secrets.sh` | Prep + 0600 secrets (no mailbox password) |
| `up.sh` / `down.sh` | Always-on compose (`restart: unless-stopped`) |
| `connect-hostinger.sh` | `read -s` → sealed credentials in Warmbly DB |
| `status.sh` | Compact PASS/FAIL board |
| `pause.sh` / `resume.sh` | Kill switch (file + governor API) |
| `backup.sh` / `restore.sh` | DB + separate key bundle |
| `prove-hostinger-net.sh` | TCP/TLS premise, no auth |
| `prove-restart.sh` | Durable state after container restart |
| `self-smoke.sh` | Requires explicit `CONFENGE_SELF_SMOKE_TO` |
| `validate.sh` | Offline pack checks |
| `release-deploy.sh` | Roll onto a release SHA using CI-built images |
| `disk-guard.sh` | Disk preflight, bounded cleanup, retention |
| `docker-gc-install.sh` | Daily GC timer + host BuildKit cache cap |
| `host-disk-report.sh` | Shared-host storage picture, read-only |

Compose: root `docker-compose.yml` + `deploy/confenge-vps/docker-compose.override.yml`
+ `deploy/confenge-vps/docker-compose.release.yml`, project name `warmbly-confenge`.

## Release model

The VPS does not compile. CI publishes one image per service per commit on main,
tagged with the commit SHA, and the release overlay pins every application
service to `ghcr.io/tjsasakifln/warmbly/<service>:<release-sha>` (Go services to
the `-minprofile` variant). Each `build:` section is reset, so compose cannot
fall back to building here. Mutable `:dev` and `:latest` are never the
deployment authority.

`up.sh` order: disk preflight, bounded cleanup if needed, pull, verify each
image's `org.opencontainers.image.revision`, write the `deploy_preflight` kill
switch, `up -d --no-build`, backend health, `pg_isready`, `verify-release.sh`
per service, clear the deploy kill switch, retention sweep. It refuses before
recreating anything if any step fails, so a bad release leaves the healthy one
running.

Step 8 clears only a `deploy_preflight` switch. An operator pause from
`pause.sh` survives a deploy and still needs `resume.sh`.

## Disk safety

The root filesystem is shared with Postgres and with co-tenant stacks. A deploy
once consumed the last free space, Postgres could not extend files and the
backend restart-looped on migrations. Guards:

- build context: `.dockerignore` excludes `data`, `ops`, `ops-evidence`,
  `backups`, archives and dependency trees. CI enforces a 150 MB budget with
  `scripts/build_context_size.py`.
- preflight: `disk-guard.sh preflight` needs a 20 GB deploy budget plus a 20 GB
  Postgres reserve and 12% free, and reclaims only reconstructible artifacts
  before refusing.
- retention: `disk-guard.sh retain` runs after every deploy and daily via
  `confenge-docker-gc.timer`. Builder cache capped at 8 GB / 168 h, dangling
  images pruned, Warmbly release images beyond the current and previous release
  removed without `-f`. Named volumes, Postgres data, `confenge_ops`,
  `confenge_keys` and `blobs` are never touched, and no co-tenant data is.
- backups: `backup.sh` keeps the newest `CONFENGE_BACKUP_KEEP` (default 10)
  archives with their manifests, checksums and key bundles.

## Safety profile (fixed)

```env
CONFENGE_OUTREACH_ENABLED=true
CONFENGE_REQUIRE_HUMAN_APPROVAL=true
CONFENGE_AUTO_SEND_ENABLED=false
CONFENGE_GREEN_AUTORUN_ENABLED=false
CONFENGE_WHATSAPP_ENABLED=false
CONFENGE_SEND_BUSINESS_DAYS_ONLY=true
CONFENGE_SEND_TIMEZONE=America/Sao_Paulo
CONFENGE_RATE_START_PER_HOUR=10
CONFENGE_RATE_MAX_PER_HOUR=20
```

Always-on execution is not unattended authorization. GREEN autorun stays off.
Startup and the worker refuse `CONFENGE_AUTO_SEND_ENABLED=true`,
`CONFENGE_GREEN_AUTORUN_ENABLED=true`, and `CONFENGE_REQUIRE_HUMAN_APPROVAL=false`.

### Live effective (2026-08-17, WARMBLY-002 / #82)

Deployed on `/opt/warmbly-confenge` (`warmbly-confenge`):

```text
SHA=d6eb0ef1a9c206807bc4c3df8b0d21b1167971c8
APP_ENV=production
CONFENGE_OUTREACH_ENABLED=true
CONFENGE_AUTO_SEND_ENABLED=false
CONFENGE_GREEN_AUTORUN_ENABLED=false
CONFENGE_REQUIRE_HUMAN_APPROVAL=true
CONFENGE_SENDING_PAUSED=true
CONFENGE_WHATSAPP_ENABLED=false
CONFENGE_WHATSAPP_AUTO_SEND_ENABLED=false
kill_switch=engaged reason=warmbly002_post_deploy
dispatch=PAUSED
inbound_health=auto_send_enabled=false dispatch_attempted=false status=READY
```

Backend and worker process env match the file profile. Local Mailpit sink stayed at 7
`CONFENGE-PA-*` messages (no new message after deploy). This deploy does not authorize
any real send. Do not run `resume.sh` from this page.

`origin/main` later moved to `4a505917` (#87 intel exception-code persist). That commit
does not touch send gates. It is not on this host yet.

## Hostinger Business Email Starter vs commercial pacing

Mailbox: `tiago.sasaki@confenge.com.br`

Product confirmed by operator: **Hostinger Business Email Starter**

- Not cPanel Email (do not apply cPanel 200/h or 2400/day figures)
- Not Free Email / Trial limits

Official provider ceiling currently documented:

```text
outgoing messages = 1000 / rolling 24h / mailbox
```

One send to one recipient normally counts as one message. Multiple recipients may consume multiple units. **hPanel** remains the source of truth if Hostinger changes the limit.

The Hostinger figure is only a **hard provider ceiling**. Do **not** raise commercial pacing toward it.

CONFENGE governor / commercial pacing (unchanged in this PR):

```text
adaptive start = 10/h
adaptive max   = 20/h
business window = 09:00–18:00
timezone = America/Sao_Paulo
business days only = true
daily operational cap = 200
```

With a nine-hour window: `20/h × 9h = 180` theoretical max/day.

```text
provider ceiling ≠ operational target

Hostinger provider max     = 1000 / rolling 24h (Business Email Starter)
CONFENGE operational max   ≈ 180/day (20/h × 9h), shell daily = 200
min(applicable limits) always wins
```

There is ample headroom under the provider ceiling. Do **not**:

- implement artificial 100/day throttling
- apply cPanel 200/h
- modify governor core in this PR
- raise `CONFENGE_RATE_MAX_PER_HOUR` above 20

Deployment documents:

```env
HOSTINGER_PLAN_CLASS=BUSINESS_EMAIL_STARTER   # operator-confirmed
# docs only unless core later wires a provider cap:
# HOSTINGER_TECHNICAL_DAILY_CAP=1000   # rolling 24h / mailbox
```

Future scale-up depends on hard/soft bounce, spam/reputation, delivery failures, reply rates, DNC, and mailbox health, not on “using more of the 1000.”

## Mail path

```text
Warmbly worker → authenticated SMTP/TLS → smtp.hostinger.com → recipient
recipient reply → Hostinger → IMAP TLS → Warmbly worker/consumer
```

No Postfix/Exim/Mailcow. No PTR-based direct send. No M365/Graph.

**Network blocker observed on Netcup:** outbound TCP 465/587 to Hostinger timed out while IMAP 993 worked (also blocked to Gmail/O365/SES submission ports). This is provider egress policy, not a local ufw rule. Unlock outbound SMTP with Netcup before production self-smoke. See `vps-execution-inventory.md`.

### Netcup Mail block (SCP firewall)

Outbound SMTP (25/465/587) is blocked by Netcup's **cloud** firewall policy **netcup Mail block**, not by guest ufw. Guest `iptables OUTPUT` remains ACCEPT while connections still time out.

Operator fix (official Netcup docs):

1. Open **Server Control Panel (SCP)** for this VPS.
2. Open the **Firewall** menu.
3. On the **netcup Mail block** policy, click **Delete**.
4. Click **Save** (applies immediately; no reboot).

Reference: [netcup Firewall / Mail block](https://www.netcup.com/en/helpcenter/documentation/server/firewall).


### Operator go-live after Netcup SMTP unlock

```bash
# On VPS as root, repo at /opt/warmbly-confenge
# 1) Netcup CCP/SCP: freischalten outbound mail (TCP 465 + 587). No local MTA.

deploy/confenge-vps/prove-hostinger-net.sh   # expect SMTP=PASS IMAP=PASS

# Or one-shot after unlock (interactive mailbox password):
export CONFENGE_OPS_PASSWORD='…'             # Warmbly UI user password
CONFENGE_SELF_SMOKE_TO=tiago.sasaki@confenge.com.br \
  deploy/confenge-vps/post-smtp-unlock.sh

# Optional full reboot drill:
# sudo reboot && … && deploy/confenge-vps/status.sh
```

Daily laptop path remains browser-only via SSH tunnel (below).

## Isolation from extra-cli

| Allowed | Forbidden |
| --- | --- |
| HTTPS feed / outcome contracts | Shared application SQL |
| host-gateway / loopback to :8443 | Warmbly reading datalake tables |
| Separate Docker volumes | Import of extra-cli models |

Outcome HMAC remains required even on loopback.

## Operator access

Simplest secure path (no new VPN product):

```bash
deploy/confenge-vps/tunnel.sh
# browser: http://127.0.0.1:5173
```

UI/API bind **127.0.0.1 only**. The dedicated CONFENGE deployment opens the
Portuguese operator dashboard directly. It silently mints a normal JWT session for
the configured technical member, so organization permissions and audit attribution
remain enforced without an interactive login. Daily path: review, approve, edit,
reject, or mark DNC in the browser. The laptop can sleep after approval because the
queue, scheduling, governor, and SMTP transport run on the VPS.

Public inbound (web-cfg) is the only application path that leaves the
host: `https://api.confenge.com.br/api/v1/webhooks/confenge/inbound` via host
nginx on 80/443, allowlisted to that POST and `GET .../health`. See
[inbound-edge.md](./inbound-edge.md).

Never expose ports 5173 or 8080 publicly while `CONFENGE_OPERATOR_MODE=true`.
The laptop tunnel maps the backend to `127.0.0.1:18080`, leaving local port
`8080` available for Evolution API. The web runtime's `API_PUBLIC_URL` must use
the same port. Run compose maintenance through `deploy/confenge-vps/compose.sh`;
it always loads the CONFENGE override and private environment file.
Changing the operator IDs requires an existing active member with view and manage
contacts permissions. Invalid identity or membership prevents backend startup.
On a new empty database, set `CONFENGE_VPS_SEED=true` for the first `up.sh` run.
The script starts the backend with operator mode temporarily disabled, loads the
deterministic technical member, then restarts with the validated mode enabled.

## Credentials

1. `gen-secrets.sh` writes KMS + credentials encryption keys (0600 `.env`).
2. `connect-hostinger.sh` prompts password with `read -s`, posts onboarding, seals in DB, discards plaintext.
3. Backups: SQL dump + **separate** secrets bundle (`backup.sh`).

Losing `KMS_LOCAL_MASTER_KEY` or `CREDENTIALS_ENCRYPTION_KEY` makes sealed mailboxes unrecoverable.

## Kill switch

```bash
deploy/confenge-vps/pause.sh "reason"
deploy/confenge-vps/resume.sh
```

Independent of extra-cli/AI. UI dispatch pause remains available when logged in.

These are exceptional controls: emergency stop, deliberate maintenance or
cutover, deployment safety, explicit operator intervention. The steady state is
the stack running 24/7 with the pause cleared and the kill switch absent, and
the business window (09:00-18:00 America/Sao_Paulo, business days) gating sends
automatically from inside the running process. Never use pause/resume as an
evening or weekend schedule, and never wire them to cron. See
`docs/confenge/first-touch-fast-lane.md` for what "running, window closed" looks
like in the transport-state projection.

## Human approve → later send

1. Message generated/imported on VPS.
2. Human opens exact content in browser, approves.
3. Approval + content hash persisted on VPS.
4. Browser disconnects; laptop may power off.
5. When `due_at` arrives, worker + governor dispatch via Hostinger (or Mailpit for tests).

If core ever requires `CONFENGE_AUTO_SEND_ENABLED=true` for already human-approved queued sends, treat as `CROSS_PR_BLOCKER` (do not bypass here).

## Validation

```bash
deploy/confenge-vps/validate.sh
deploy/confenge-vps/prove-hostinger-net.sh   # on VPS
deploy/confenge-vps/prove-restart.sh         # with stack up
```
