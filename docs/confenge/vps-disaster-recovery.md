# CONFENGE VPS disaster recovery

## What to back up

| Asset | Where | Notes |
| --- | --- | --- |
| Postgres | `deploy/confenge-vps/backup.sh` → `warmbly_dev.sql` | Approvals, queues, sealed creds, outcomes |
| Encryption roots | secrets bundle `keys-*.env` (0600) | `KMS_LOCAL_MASTER_KEY`, `CREDENTIALS_ENCRYPTION_KEY`, `AUTH_SECRET`, tokens |
| Redacted env | `env.redacted` inside archive | Non-secret config snapshot |
| Backup inventory | `MANIFEST.json` and `SHA256SUMS` inside archive | Hashes, sizes, versions, schema class, and structural counts only |
| confenge_ops volume | included indirectly via kill-switch host mirror | Prefer DB governor state |
| Asaas transport queue | `asaas-events.sqlite3` inside the archive | Online read-only SQLite backup; payload minimized, blocked/dead retained |

**Do not back up:** plaintext Hostinger password, extra-cli datalake, unlimited logs, `node_modules`.

**Do not store** SQL dump and key bundle in the same public object store without encryption. Prefer offline or encrypted vault for keys; SQL can live in a separate private store.

## Backup

```bash
# on VPS, repo at /opt/warmbly-confenge
deploy/confenge-vps/backup.sh
# → data/backups/confenge-vps/warmbly-confenge-<ts>.tar.gz
# → data/backups/confenge-vps/warmbly-confenge-<ts>.tar.gz.manifest.json
# → data/backups/confenge-vps/warmbly-confenge-<ts>.tar.gz.sha256
# → data/backups/confenge-vps/secrets/keys-<ts>.env
# → data/backups/confenge-vps/secrets/keys-<ts>.env.sha256
```

The script preflights a present Asaas SQLite schema before starting `pg_dump`.
It accepts the current versioned schema and the known unversioned legacy schemas
without initializing `Queue`. An unknown version, unknown column layout, missing
`events` table, or SQLite integrity error fails early with no PostgreSQL dump.

The PostgreSQL dump uses one `pg_dump` MVCC snapshot. The SQLite copy uses
`sqlite3.Connection.backup` with the source opened in `mode=ro`, so WAL writers
can continue. The backup path does not run DDL, recover leases, insert a backup
row, or change queue states and timestamps. `MANIFEST.json` contains no payload,
secret, hostname, email address, or operator identity.

Validate both checksum layers before moving an archive:

```bash
ARCHIVE=data/backups/confenge-vps/warmbly-confenge-<ts>.tar.gz
(cd "$(dirname "$ARCHIVE")" && sha256sum -c "$(basename "$ARCHIVE").sha256")

PROOF_DIR="$(mktemp -d)"
tar -xzf "$ARCHIVE" -C "$PROOF_DIR"
(cd "$PROOF_DIR" && sha256sum -c SHA256SUMS)
ASAAS_ADAPTER_DB="$PROOF_DIR/asaas-events.sqlite3" \
  python3 deploy/confenge-vps/asaas-adapter/adapter.py preflight
```

Never point a schema test at the live file first. The tracked regression fixture is
`deploy/confenge-vps/asaas-adapter/testdata/legacy-stateless-v0.sql`.

Schedule the backup daily under root after the business window. A backup does
not pause a campaign, call Asaas, send mail, or resume any commercial flow.

## Restore

1. Install Docker stack (`install.sh`, `gen-secrets.sh` **or** restore keys from secrets bundle into `deploy/confenge-vps/.env` mode 0600).
2. Start PostgreSQL only, then create or recreate an empty `warmbly_dev`. The
   restore script refuses a non-empty target before applying any SQL.
3. `deploy/confenge-vps/restore.sh /path/to/archive.tar.gz` (destructive to the selected empty database and adapter target). The script verifies the manifest and checksums, validates either supported adapter schema, and restores it with mode 0600 under a 0700 state directory.
4. Restart backend/worker: `docker compose ... restart backend worker`.
5. `deploy/confenge-vps/status.sh`
6. Confirm mailbox still decrypts (list accounts / trigger IMAP). Do **not** re-enter password unless keys were wrong era.

## Controlled restore proof

Run every proof against a disposable Compose project or a separate proof host.
Do not use the production project name, PostgreSQL database, adapter path, or
secrets file. The proof database must be empty.

```bash
export COMPOSE_PROJECT_NAME=warmbly-restore-proof-186
export CONFENGE_VPS_ENV=/path/to/proof-only.env
export CONFENGE_RESTORE_DATABASE=warmbly_restore_proof_186
export CONFENGE_RESTORE_ASAAS_DB=/tmp/warmbly-restore-proof-186/events.sqlite3

# proof-only.env must set the same unique COMPOSE_PROJECT_NAME value.
# Start only the disposable PostgreSQL service, then create an empty target.
docker compose -p "$COMPOSE_PROJECT_NAME" \
  -f docker-compose.yml -f deploy/confenge-vps/docker-compose.override.yml \
  --env-file "$CONFENGE_VPS_ENV" up -d postgres
docker compose -p "$COMPOSE_PROJECT_NAME" \
  -f docker-compose.yml -f deploy/confenge-vps/docker-compose.override.yml \
  --env-file "$CONFENGE_VPS_ENV" exec -T postgres \
  createdb -U warmbly "$CONFENGE_RESTORE_DATABASE"

deploy/confenge-vps/restore.sh /path/to/warmbly-confenge-<ts>.tar.gz
# RESTORE_PROOF=PASS postgres_database=warmbly_restore_proof_186

# PostgreSQL opens and carries the migration marker from the dump.
docker compose -p "$COMPOSE_PROJECT_NAME" \
  -f docker-compose.yml -f deploy/confenge-vps/docker-compose.override.yml \
  --env-file "$CONFENGE_VPS_ENV" exec -T postgres \
  psql -U warmbly -d "$CONFENGE_RESTORE_DATABASE" -Atqc \
  'SELECT version,dirty FROM schema_migrations ORDER BY version DESC LIMIT 1'

# SQLite opens, passes integrity/schema validation, and keeps structural counts.
ASAAS_ADAPTER_DB="$CONFENGE_RESTORE_ASAAS_DB" \
  python3 deploy/confenge-vps/asaas-adapter/adapter.py preflight
```

Compare the SQLite `schema`, `table_counts`, and `queue_state_counts` output to
`MANIFEST.json`. Compare the restored file hash to the archived
`asaas-events.sqlite3` hash. Inspect `env.redacted` for `***REDACTED***`, and
keep the key bundle outside the extracted archive. The key bundle is required
for a future live restore, but no proof should attempt to decrypt a real
mailbox.

After recording the proof, remove only the disposable project and proof
directory. A live restore remains operator-scheduled and requires a commercial
reconciliation window.

## Failure and rollback

- A preflight failure happens before `pg_dump`. Keep the current release, copy
  the SQLite file with an approved online mechanism, and update the fixture and
  classifier in code. Do not migrate the live file to make the backup pass.
- A PostgreSQL or packaging failure removes the private stage and incomplete
  archive outputs. The Asaas source remains read-only.
- A restore-proof failure affects only the disposable targets. Delete those
  targets, retain the last known-good archive, and investigate the manifest or
  component named in the error.
- Never pause or resume a campaign as part of backup recovery. Never call an
  Asaas mutation endpoint. Restoring the production queue requires an explicit
  operator window, adapter stop, post-restore financial reconciliation, and
  rollback to the prior application SHA if the restored schema cannot start.

## After VPS reboot

Docker + `restart: unless-stopped` bring the stack back. No laptop `make`. Verify:

```bash
deploy/confenge-vps/status.sh
```

## Emergency stop

```bash
deploy/confenge-vps/pause.sh "incident"
# or from browser: dispatch pause when available
```

## Provider accepted but local completion failed

An `attempted` dispatch is terminal for enqueue. Do not change it back to
`queued`, replay the campaign task, or insert a `sent` row directly. First
confirm provider acceptance and capture the original Message-ID and timestamp.
Keep dispatch paused while reconciling.

The backend image contains the fail-closed operator command. Its dry run checks
the task, campaign enrollment, touchpoint, recipient, mailbox, reservation,
queue hand-off, provider Message-ID uniqueness, and organization actor without
calling the worker or provider:

```bash
TASK_ID=<original-task-uuid>
TOUCHPOINT_ID=<original-touchpoint-uuid>
RECIPIENT=<exact-original-recipient>
PROVIDER_MESSAGE_ID='<provider-message-id>'
ACCEPTED_AT=<provider-acceptance-rfc3339>
ACTOR_ID=<accepted-organization-member-uuid>

deploy/confenge-vps/compose.sh run --rm --no-deps \
  --entrypoint /app/confenge backend \
  reconcile-provider-accepted \
  --task-id "$TASK_ID" \
  --touchpoint-id "$TOUCHPOINT_ID" \
  --recipient "$RECIPIENT" \
  --provider-message-id "$PROVIDER_MESSAGE_ID" \
  --accepted-at "$ACCEPTED_AT" \
  --actor "$ACTOR_ID" \
  --dry-run
```

Apply only after the dry-run bindings match the provider evidence. The command
reuses normal campaign completion, preserves the provider acceptance time,
updates campaign progress only when it is missing, and writes an append-only
audit entry with `transport_invoked=false`.

```bash
deploy/confenge-vps/compose.sh run --rm --no-deps \
  --entrypoint /app/confenge backend \
  reconcile-provider-accepted \
  --task-id "$TASK_ID" \
  --touchpoint-id "$TOUCHPOINT_ID" \
  --recipient "$RECIPIENT" \
  --provider-message-id "$PROVIDER_MESSAGE_ID" \
  --accepted-at "$ACCEPTED_AT" \
  --actor "$ACTOR_ID" \
  --confirm PROVIDER_ACCEPTED_LOCAL_COMPLETION_FAILED
```

Re-running the same command is idempotent. A different task, recipient,
touchpoint, mailbox, provider Message-ID, or non-accepted queue state is refused.
Resume dispatch only after the touchpoint is `SENT`, the queue and reservation
are committed, `confenge_dispatch_sends` has the original task, and the audit
entry contains the original provider correlation.

## Network / provider incidents

| Symptom | Action |
| --- | --- |
| HOSTINGER SMTP FAIL | Confirm Netcup outbound 465/587; do not install MTA |
| HOSTINGER IMAP FAIL | Check DNS/firewall; worker logs |
| EXTRA FEED FAIL/STALE | Check `/opt/confenge-plane` + extra-cli feed generation |
| OUTCOME LOOP FAIL | Check receptor systemd unit + nginx 8443 proxy |
| ASAAS ADAPTER UNKNOWN | Check the versioned systemd unit and loopback `:8791`; do not infer provider delivery |
| Asaas `blocked` / `dead` | Inspect occurrence code/owner/next action, then compare the provider Webhook Log before replay |
| Asaas queue interrupted | Fix the endpoint first, then reactivate in Asaas; provider events older than 14 days may be irrecoverable |

The [Asaas webhook FAQ](https://docs.asaas.com/docs/faq-de-webhooks) states that
webhook events remain available for up to 14 days and that 15 consecutive
failures interrupt the queue. The local queue is therefore recovery state, not
a cache. Daily backup freshness is exposed by the adapter health endpoint.
Restore is exercised in isolation by
`deploy/confenge-vps/asaas-adapter/test_adapter.py`; a production restore still
requires an operator window and financial reconciliation after restart.

## Parallel hardening

This deployment pack must not patch `green_autorun`, policy auth, touchpoint SM, or concurrent migrations. Authorization defects → `CROSS_PR_BLOCKER` for the hardening PR.
