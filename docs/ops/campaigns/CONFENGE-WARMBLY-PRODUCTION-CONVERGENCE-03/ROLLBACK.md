# Rollback — CONFENGE-WARMBLY-PRODUCTION-CONVERGENCE-03

Keep the host lock `/var/lock/confenge-production-deploy` during rollback.

Rollback if any of:

- raw query or query_hash appears on chain, INBOUND NOW, alert, scoreboard, feedback, report, logs, API, or CLI
- a search observation is accepted but discovery does not persist
- the scoreboard infers discovery counts from leads
- synthetic ingest sends operator email
- migration 000106 breaks boot
- `/ready` fails
- auto-send or kill switch diverges from fail-closed
- deployed SHA is not origin/main

Procedure:

1. Keep the host lock.
2. Restore `/opt/warmbly-confenge` to the previous SHA recorded in DEPLOY-EVIDENCE.json.
3. Rebuild the minprofile image and run `deploy/confenge-vps/up.sh`.
4. Prefer keeping the additive 000106 schema if rows exist. Down-migrate only after confirming no observation rows and a pg_dump backup.
5. Validate `/health`, `/live`, `/ready`, inbound health READY, auto_send=false, kill switch paused.
6. Record cause, before SHA, after SHA.

Do not delete real receipts.
