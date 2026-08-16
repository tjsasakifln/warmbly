# Upstream maintenance notes (CONFENGE outreach)

Fork: `tjsasakifln/warmbly` · Upstream: `warmbly/warmbly`

Canonical classification and SHA snapshot: [fork-drift-audit.md](./fork-drift-audit.md) / [fork-drift-audit.json](./fork-drift-audit.json).

This page is the merge/rebase contract. It does not try to land every fork feature on `warmbly/warmbly`.

## Recorded SHAs (revalidate before every sync)

Audited 2026-08-16T17:47:24Z:

- `origin/main` = `2444058ef3a64774b84eea57425b731700946b84` (85 ahead)
- `upstream/main` = `7521575cef47200c7c165afdbbba8e075e6b1ac1` (16 behind)
- merge-base = `0ae4db2c4114404705735c480101250467f4fe6c`

If `origin/main` or `upstream/main` moved, refresh the audit JSON before merging. Do not trust this page alone.

## Cadence

1. **Weekly fetch, no merge.** `git fetch origin main && git fetch upstream main`. Record left-right counts. Do nothing else unless a core file below changed upstream.
2. **Sync only on a dedicated branch** cut from current `origin/main`. Never from a dirty last-mile tree.
3. **Prefer merge of `upstream/main` into the fork**, not a rebase of the 85 published fork commits. Rebase would rewrite PRs and VPS deploy SHAs.
4. **Never automatic cherry-pick or `git pull upstream`.** A GitHub workflow that syncs from `warmbly/warmbly` must run:
   - `go test` on `./internal/app/confenge/...` `./cmd/consumer/` (CONFENGE wire) and any new `fork_sync_boundary` tests
   - the existing `confenge-product-acceptance` job
   Boundary tests fail the PR if a sync workflow is added without that suite.
5. After the merge: `gofmt`, `make lint`, `go test ./internal/app/confenge/ ./cmd/consumer/ -count=1`, then the product-acceptance job.

Weekly is enough. Daily auto-sync would burn the high-churn files below for no action-plane gain.

## What stays fork-only (do not offer upstream)

KEEP_CONFENGE and ISOLATE rows in the audit JSON: `/app/confenge`, outreach migrations `000083`–`000101`, inbound webhook, kill switch, VPS pack, WhatsApp/Evolution, CRM bootstrap, operator mode, GREEN autorun, intel executive view.

UPSTREAMABLE later, as their own PRs, without CONFENGE:

- IMAP EXISTS/UIDNEXT fallback (generic)
- nanoid 3.3.18 pin

## What to take from upstream

The 16 commits after `0ae4db2c` (self-host/OAuth/Gmail/timezone/warmblyctl/#114–#129). Full SHAs are in the audit JSON. They fix Warmbly self-host. They are not CONFENGE features.

## Conflict forecast (next real sync)

### Migration number band (hard)

Fork last on `origin/main`: `000101_outreach_commercial_intel`.
Upstream after merge-base:

- `000083_instance_settings`
- `000084_email_account_timezone_unset`

The fork already used `000083`/`000084` for outreach staging/drafts. Those rows are applied on the live VPS. **Do not renumber fork files.** On merge, take the upstream SQL and save it as new fork numbers (`000102`+; bump if last-mile `000102` has landed). Pair each up/down.

Stale note from an earlier draft of this page cited `000080`. That number is upstream `default_free_trial_plan` and is already on both sides of the merge-base. The live collision is `000083`/`000084`.

### High-churn files (both sides modified)

```
cmd/backend/main.go
internal/api/routes.go
internal/api/handler/handler.go
internal/app/worker/wmail/send.go
internal/models/token.go
deploy/docker/backend.Dockerfile
docker-compose.yml
Makefile
README.md
docs/content/docs/api/endpoints.mdx
docs/content/docs/api/error-codes.mdx
web/src/components/ui/field.tsx
web/src/lib/api/client/Request.ts
web/src/lib/api/client/normalizeError.ts
```

Resolution rule: take upstream behavior, then re-insert the CONFENGE needles (service construct, `/confenge` group, inbound webhook, worker kill-switch, `/app/confenge` route). Do not take a "ours" or "theirs" wholesale on `main.go` or `routes.go`.

### Upstream-only surfaces that will appear

- `cmd/warmblyctl` (distinct from `cmd/confenge`)
- instance settings + mailbox timezone columns
- Gmail raw send, history checkpoint, thread_id
- `/data/blobs` ownership in Dockerfiles
- tracking dedupe SQL

## Files introduced (fork, stay isolated)

| Area | Paths |
| --- | --- |
| Migrations | `000083`–`000101` outreach/confenge (see audit JSON) |
| Models | `internal/models/outreach.go`, `outreach_inbound.go`, `whatsapp.go` |
| App | `internal/app/confenge/*`, `internal/app/whatsapp/*` |
| Repository | `internal/repository/pg_outreach*.go`, `pg_whatsapp.go`, `pg_confenge_policy.go` |
| API | `internal/api/handler/confenge*.go`; routes in `routes.go`; handler fields |
| Wire | `cmd/backend/main.go`, `cmd/consumer/main.go` |
| CLI | `cmd/confenge` |
| UI | `web/src/app/app/confenge`, AppNav, `web/src/main.tsx` path `confenge` |
| CI | `.github/workflows/ci.yml` job `confenge-product-acceptance` |
| Docs | `docs/confenge/*` |

## Extension points used

- Feature flag via env (no change to billing/feature gate matrix)
- Org-scoped routes with existing `RequireAccess` / contact permissions
- Audit spine (`AuditEntityOutreachImportRun`, `AuditEntityOutreachAccount`)
- Embedded migrations (same as other features)
- SSRF client (`internal/pkg/safehttp`) for remote feeds

## Disable

Set `CONFENGE_OUTREACH_ENABLED=false` (default). Routes still expose
`GET /confenge/status` with `"enabled": false`. Import/list return not found.

`CONFENGE_AUTO_SEND_ENABLED` must stay `false`. `CONFENGE_GREEN_AUTORUN_ENABLED` must stay `false`. The kill switch (`make confenge-stop-sending` / `deploy/confenge-vps/pause.sh`) remains the operator halt.

## Remove customization

1. Drop env vars and docs.
2. Delete `internal/app/confenge`, handler, repo, models, migration (new down).
3. Remove wiring in `main.go` / `handler.go` / `routes.go` / realtime spine / AppNav / `web/src/main.tsx`.

Do not leave staging tables with PII if decommissioning a deployment: export then
drop via down migration after backup.

Do not do this as part of an upstream sync. Decommission is a human decision.
