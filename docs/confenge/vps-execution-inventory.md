# VPS execution inventory (sanitized)

Captured 2026-08-09 from live Netcup host. No secrets.

## Warmbly repo

| Item | Value |
| --- | --- |
| Branch base | `main` @ `35a8d14b` (pre-hardening; this pack is parallel-safe) |
| Deployment branch | `feat/confenge-vps-execution-plane` |
| Project name | `warmbly-confenge` |
| Intended path | `/opt/warmbly-confenge/` |

## Host

| Item | Value |
| --- | --- |
| Hostname | `v2202607385716487230` |
| Public IPv4 | `159.195.18.88` |
| Distro | Debian 13 (trixie) |
| Kernel | 6.12.x amd64 |
| CPU | 8 vCPU |
| RAM | 15 GiB (~10 GiB available at sample) |
| Swap | 4 GiB file (`/swapfile`), partially used at sample |
| Disk | 503 GiB root, ~435 GiB free (~10% used) |
| Docker | 26.1.5 |
| Compose | v2.32.4 |

## Co-tenants (must stay isolated)

| Component | Path / port | Notes |
| --- | --- | --- |
| extra-cli | `/opt/extra-consultoria`, datalake `/var/lib/extra-consultoria` (~18G) | Intelligence plane |
| confenge-plane feed | `/opt/confenge-plane`, HTTPS **8443** | `confenge.outreach.v1` + outcome proxy |
| outcome receptor | loopback **8790** (systemd `confenge-outcome-receptor`) | HMAC; not public |
| Host Postgres 17 | `127.0.0.1:5432` | **extra-cli** DB only; Warmbly uses its own Docker volume |
| SSH | **2222** | Operator access |
| Node exporter | 9100 | Monitoring |

## Existing warmbly-confenge state (pre this PR)

Legacy compose project containers were **stopped** with volumes intact:

- `warmbly-confenge_postgres_data`, `redis_data`, `nats_data`, `blobs`
- Prior overlay had `CONFENGE_GREEN_AUTORUN_ENABLED=true` and leftover M365 env keys; **this pack supersedes** that profile (GREEN off, Hostinger-only, no Graph)

Do not delete volumes without a restore plan.

## Firewall (ufw)

Allow: 22, 2222, 8443. Deny public 8790. Docker bridge may reach 8790.

Warmbly UI/API/Postgres/Redis/NATS must remain **loopback-bound** (this pack enforces `127.0.0.1` publishes).

## Hostinger network premise

| Endpoint | Result from VPS |
| --- | --- |
| DNS smtp/imap.hostinger.com | Resolves (Cloudflare) |
| TCP smtp:465 | **FAIL** (timeout) |
| TCP smtp:587 | **FAIL** (timeout) |
| TCP imap:993 | **PASS** (+ TLS cert CN=hostinger.com) |

Local operator machine: SMTP 465/587 and IMAP 993 all OPEN.

**Conclusion:** Netcup (or upstream) blocks outbound SMTP submission from this VPS. IMAP is ready. Production Hostinger SMTP requires provider unlock of outbound 465/587. No local MTA.

## Capacity headroom (sample idle)

| Metric | Sample |
| --- | --- |
| Load | ~0.02–0.16 |
| Mem available | ~10 GiB |
| Swap used | ~2.3 GiB (pre-existing pressure; keep Warmbly limits) |
| Disk free | ~435 GiB |

Warmbly resource limits in override (~6–8 GiB ceiling if all hit limits) leave room for extra-cli if scans stay bounded. If national scans drive swap thrash / OOM, status is `BLOCKED_VPS_CAPACITY` with fresh metrics (do not soft-pass).

## Hostinger plan class (operator-confirmed)

| Field | Value |
| --- | --- |
| Mailbox | `tiago.sasaki@confenge.com.br` |
| Product | **Hostinger Business Email Starter** (not cPanel, not Free/Trial) |
| `HOSTINGER_PLAN_CLASS` | `BUSINESS_EMAIL_STARTER` |
| Provider ceiling | **1000** outgoing messages / rolling 24h / mailbox (hPanel SoT) |
| CONFENGE operational pace | adaptive **10→20/h**, daily shell **200**, window 09–18 America/Sao_Paulo |
| Theoretical ops max | ~180/day (20×9h) under provider 1000/day headroom |

provider ceiling ≠ operational target. Do not apply cPanel 200/h.


## Live pack proof (2026-08-09, sanitized)

Stack brought up with `deploy/confenge-vps` overlay (existing images, no extra-cli disruption):

| Check | Result |
| --- | --- |
| Backend/worker/consumer/postgres/redis/nats | PASS |
| Hostinger IMAP 993 | PASS |
| Hostinger SMTP 465/587 | FAIL (provider egress; all common SMTP targets blocked) |
| EXTRA FEED :8443 | PASS |
| OUTCOME LOOP | PASS |
| GREEN / WhatsApp | OFF |
| DISPATCH pause/resume file kill-switch | PASS |
| Container restart persistence (DB marker) | PASS |
| Isolated restore into `warmbly_restore_proof` | PASS (185 public tables; probe row present) |
| Public bind of app ports | None (127.0.0.1 only) |
| Resource sample | ~5 GiB used / 15 GiB; Warmbly containers well under limits |

**Operator still required for:** Netcup SMTP unlock; interactive `connect-hostinger.sh` (mailbox password); explicit self-smoke `CONFENGE_SELF_SMOKE_TO`; optional full VPS reboot drill.

## Netcup Mail block (root cause of SMTP FAIL)

Guest ufw/iptables do **not** block outbound 465/587 (OUTPUT policy ACCEPT). Connectivity still fails because Netcup SCP cloud firewall applies the default policy **netcup Mail block** (blocks inbound and outbound SMTP).

Operator fix (official):

1. SCP → server → **Firewall**
2. Delete **netcup Mail block**
3. **Save**

Then: `deploy/confenge-vps/prove-hostinger-net.sh` should show HOSTINGER_SMTP=PASS.

Reference: https://www.netcup.com/en/helpcenter/documentation/server/firewall


## Network proof update (2026-08-09 after SCP Mail block delete)

Via Playwright: Netcup SCP → Firewall → Delete **netcup Mail block** → Save.

| Endpoint | Result |
| --- | --- |
| TCP smtp.hostinger.com:465 | **OPEN** |
| TCP smtp.hostinger.com:587 | **OPEN** |
| TCP imap.hostinger.com:993 | **OPEN** |
| status.sh HOSTINGER SMTP | **PASS** |
| status.sh HOSTINGER IMAP | **PASS** |
| Full stack status | all PASS (GREEN OFF) |

Mailbox `tiago.sasaki@confenge.com.br` not yet sealed in Warmbly DB (seed accounts only). Next: interactive `connect-hostinger.sh` + optional sink self-smoke.

## Mailbox connect (same day)

- Stable `WORKER_ID=10c8f5e4-1c39-5b2a-9c8b-3d2f0a8b1a01` required so backend can route validate/send (compose override).
- Hostinger app password generated in hPanel (not committed); sealed via `connect-hostinger.sh`.
- Account `tiago.sasaki@confenge.com.br` status **active** after SMTP unlock.
