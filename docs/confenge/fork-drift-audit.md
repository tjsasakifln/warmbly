# Fork vs upstream drift audit (WARMBLY-013)

Machine-readable twin: [fork-drift-audit.json](./fork-drift-audit.json).
Strategy, cadence, and conflict forecast: [upstream-maintenance.md](./upstream-maintenance.md).

## Recorded SHAs (recounted 2026-08-17T03:55:00Z)

| Ref | SHA |
| --- | --- |
| `origin/main` (`tjsasakifln/warmbly`) | `7a69403cf780f57d3220a252ad2cddc54a1dfb79` |
| `upstream/main` (`warmbly/warmbly`) | `7521575cef47200c7c165afdbbba8e075e6b1ac1` |
| merge-base | `0ae4db2c4114404705735c480101250467f4fe6c` |
| left-right | 90 ahead / 16 behind |
| Plan-time base | `2444058ef3a64774b84eea57425b731700946b84` (moved) |

Recount only. No upstream merge. `#47` `#82` `#87` `#88` `#83` landed on the fork after the first audit. `000102` is on `origin/main` (exception codes). Next real sync still replays upstream SQL as `000103+`. Do not renumber applied fork migrations.

## What this audit is for

Warmbly on this fork is the action plane: approved action, delivery, reply, outcome, next action. It is not extra-cli, not web-cfg, not a CRM or forecast.

The job is to cheapen the next real sync, not to keep every fork feature on the upstream path.

## Counts

- Fork-only commits after merge-base: 90
- Upstream-only commits after merge-base: 16
- Fork-added files: 392
- Fork-modified files: 58
- Same-path modified on both sides: 14
- Fork migrations on main: `000083`–`000102`
- CONFENGE Go build tags: none (repo tags are `kafka` / `!kafka` / `tools` only)

## Classification summary

| Decision | Meaning | On operational path? |
| --- | --- | --- |
| KEEP_CONFENGE | Stay on the fork. Required for the action plane or its fail-closed gates. | Usually yes |
| UPSTREAMABLE | Narrow, generic Warmbly fix. Offer upstream later. Do not bundle CONFENGE. | IMAP/nanoid yes; the 16 upstream commits are inbound |
| ISOLATE | Keep in-tree but off the upstream merge story. DROP only with a human owner. | Mixed |
| DROP | Do not try to land this on `warmbly/warmbly`. Live deletes need a human. | No |
| RISK | Conflict or safety surface. Resolve by hand on every sync. | Usually yes |

Every material row (clusters, directories, migrations, APIs, build tags, core patches) lives in the JSON with owner, decision, risk, and `on_operational_path`.

## Keep on the operational path

- `/app/confenge` page, router path, AppNav entry
- `/confenge` API group: review, approve, queue, dispatch pause/resume, today, inbound, outcomes
- Inbound HMAC webhook + READY/BLOCKED health
- Kill switch (`CONFENGE_SENDING_PAUSED`, kill-switch file, worker re-check)
- `CONFENGE_AUTO_SEND_ENABLED` default false; `ValidateStartup` rejects true
- `CONFENGE_REQUIRE_HUMAN_APPROVAL` default true
- Migrations `000083`–`000101` as already applied (do not renumber)
- `cmd/confenge`, `internal/app/confenge`, consumer reply wiring

## Isolate (do not put on the upstream path)

- `deploy/confenge-vps` and GO-LIVE SHA cards
- WhatsApp / Evolution (`internal/app/whatsapp`, `deploy/evolution`, Evolution webhook)
- `POST /confenge/crm/bootstrap` and the 12-stage "CONFENGE Comercial" pipeline
- Dynamic priority (default off)
- Operator-mode session (loopback only; RISK if public)

WhatsApp and CRM bootstrap are DROP candidates. They stay classified, not deleted. A human owner must confirm they are unused on the live VPS before any removal. That is why this campaign stops at `READY_BEHIND_HUMAN_GATE`.

## Upstreamable (narrow)

- IMAP EXISTS/UIDNEXT fallback (generic robustness), without Hostinger branding
- nanoid 3.3.18 pin

## Take from upstream on the next sync (the 16)

1. `734cb5fe` #114 self-host invite_only + warmblyctl + instance settings
2. `1a7cdc95` #115 seal OAuth tokens
3. `8d2968ea` #116 OAuth `API_PUBLIC_URL`
4. `cdf20019` #117 OAuth callback origin
5. `16b672e6` #118 Gmail `OnTokenRefresh`
6. `4e14df16` #119 Gmail raw RFC 5322
7. `2c56dc90` #120 Gmail history persist
8. `7f1f46ea` #121 Gmail history resume
9. `ec4cd316` #122 Unibox thread_id
10. `248a32db` #123 tracking dedupe
11. `ee61c19f` #124 `/data/blobs` ownership
12. `fe9a21a7` #126 mailbox timezone
13. `f0846eb0` #125 timezone follow-up
14. `bd8545a1` #127 timezone follow-up
15. `619f4fd9` #128 warmblyctl docs
16. `7521575c` #129 warmblyctl status order

These are not CONFENGE. They are Warmbly self-host/OAuth/Gmail/timezone fixes. Take them with a merge, then re-insert CONFENGE wiring. Do not auto cherry-pick.

## Hard conflicts on the next sync

- Migration numbers `000083`/`000084` (fork outreach vs upstream instance_settings + timezone)
- `cmd/backend/main.go`, `internal/api/routes.go`, `internal/api/handler/handler.go`
- `internal/app/worker/wmail/send.go` (fork kill-switch vs upstream Gmail raw send)
- `internal/models/token.go`, `web/src/lib/api/client/Request.ts`, `web/src/components/ui/field.tsx`

## Forbidden

- Automatic cherry-pick or `git pull upstream` from GitHub Actions without `go test` on CONFENGE packages **and** the `confenge-product-acceptance` job
- Treating fixture, dry-run, or a local merge as proof that production drift is gone
- Flipping `CONFENGE_AUTO_SEND_ENABLED` or `CONFENGE_GREEN_AUTORUN_ENABLED`
- Inferring identity, consent, pipeline, or revenue
