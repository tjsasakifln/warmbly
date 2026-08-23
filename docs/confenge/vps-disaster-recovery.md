# CONFENGE VPS disaster recovery

## What to back up

| Asset | Where | Notes |
| --- | --- | --- |
| Postgres | `deploy/confenge-vps/backup.sh` → `warmbly_dev.sql` | Approvals, queues, sealed creds, outcomes |
| Encryption roots | secrets bundle `keys-*.env` (0600) | `KMS_LOCAL_MASTER_KEY`, `CREDENTIALS_ENCRYPTION_KEY`, `AUTH_SECRET`, tokens |
| Redacted env | `env.redacted` inside archive | Non-secret config snapshot |
| confenge_ops volume | included indirectly via kill-switch host mirror | Prefer DB governor state |
| Asaas transport queue | `asaas-events.sqlite3` inside the archive | Online SQLite backup; payload minimized, blocked/dead retained |

**Do not back up:** plaintext Hostinger password, extra-cli datalake, unlimited logs, `node_modules`.

**Do not store** SQL dump and key bundle in the same public object store without encryption. Prefer offline or encrypted vault for keys; SQL can live in a separate private store.

## Backup

```bash
# on VPS, repo at /opt/warmbly-confenge
deploy/confenge-vps/backup.sh
# → data/backups/confenge-vps/warmbly-confenge-<ts>.tar.gz
# → data/backups/confenge-vps/secrets/keys-<ts>.env
```

Schedule (example): daily cron under root, after business window.

## Restore

1. Install Docker stack (`install.sh`, `gen-secrets.sh` **or** restore keys from secrets bundle into `deploy/confenge-vps/.env` mode 0600).
2. `deploy/confenge-vps/up.sh` until postgres healthy.
3. `deploy/confenge-vps/restore.sh /path/to/archive.tar.gz` (destructive to current DB and adapter queue). The script validates the adapter schema and restores it with mode 0600 under a 0700 state directory.
4. Restart backend/worker: `docker compose ... restart backend worker`.
5. `deploy/confenge-vps/status.sh`
6. Confirm mailbox still decrypts (list accounts / trigger IMAP). Do **not** re-enter password unless keys were wrong era.

## Controlled restore proof

```bash
# With stack up and non-production data:
deploy/confenge-vps/backup.sh
# Note archive path and secrets path (do not commit)
# Insert probe: deploy/confenge-vps/prove-restart.sh already writes a probe table
deploy/confenge-vps/restore.sh data/backups/confenge-vps/.../warmbly_dev.sql
# Expect probe row or known approval still present
```

If restore cannot be run on the live VPS safely, run against a disposable compose project name and document the result; live VPS restore remains operator-scheduled.

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

The Asaas provider retains webhook events for up to 14 days and interrupts a
queue after 15 consecutive failures. The local queue is therefore recovery
state, not a cache. Daily backup freshness is exposed by the adapter health
endpoint. Restore is exercised in isolation by
`deploy/confenge-vps/asaas-adapter/test_adapter.py`; a production restore still
requires an operator window and financial reconciliation after restart.

## Parallel hardening

This deployment pack must not patch `green_autorun`, policy auth, touchpoint SM, or concurrent migrations. Authorization defects → `CROSS_PR_BLOCKER` for the hardening PR.
