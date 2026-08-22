# Runbook: dispatch the first bounded email cohort

Authorized host: Netcup `v2202607385716487230` `/opt/warmbly-confenge`.
Run inside the campaign send window: Mon-Fri 09:00-18:00 `America/Sao_Paulo`.
Outside it nothing transmits, and a grant that expires before the next window
cannot produce a real send.

All commands run the `confenge` binary shipped inside the backend image.

## 0. Preconditions

```bash
cd /opt/warmbly-confenge
cat .deployed_sha                       # must equal origin/main
docker exec warmbly-confenge-backend-1 env | grep CONFENGE_REPOSITORY_SHA
```

The campaign shell must be `active`; it ships as `draft` and the scheduler only
selects `status = 'active'`.

```bash
docker exec -i warmbly-confenge-postgres-1 psql -U warmbly -d warmbly_dev \
  -c "SELECT id, status, days, start_time, end_time, timezone FROM campaigns;"
```

## 1. Pull and import the fresh feed

`file://` feeds are refused in production. Use the feed plane over HTTPS.

```bash
docker exec warmbly-confenge-backend-1 /app/confenge import \
  --feed https://confenge-feed:8443/controlled-email-cohort-fresh.json \
  --org-id 22222222-0000-0000-0000-000000000001 --dry-run

docker exec warmbly-confenge-backend-1 /app/confenge import \
  --feed https://confenge-feed:8443/controlled-email-cohort-fresh.json \
  --org-id 22222222-0000-0000-0000-000000000001
```

## 2. Freeze the cohort

Pass `--feed` and `--org-id` together. The feed gives identity and scope; Postgres
gives the account and candidate ids the dispatch path needs. Either flag alone
produces a manifest that cannot both pass review and dispatch.

```bash
docker exec warmbly-confenge-backend-1 /app/confenge cohort prepare \
  --feed /data/confenge-ops/confenge.outreach.v1.json \
  --org-id 22222222-0000-0000-0000-000000000001 \
  --out /data/confenge-ops/first-cohort-bound.json \
  --limit 50 --max-daily 50 --ttl 24h
```

Hashes are derived. Never copy one by hand. The manifest holds operational PII:
it stays on the host volume and is never committed.

## 3. Authorize

```bash
docker exec warmbly-confenge-backend-1 /app/confenge cohort authorize \
  --manifest /data/confenge-ops/first-cohort-bound.json \
  --actor <FOUNDER_UUID> \
  --org-id 22222222-0000-0000-0000-000000000001          # dry run first

docker exec warmbly-confenge-backend-1 /app/confenge cohort authorize \
  --manifest /data/confenge-ops/first-cohort-bound.json \
  --actor <FOUNDER_UUID> \
  --org-id 22222222-0000-0000-0000-000000000001 --confirm
```

Authorization is all-or-nothing. `authorized_touchpoints` must equal
`selected_accounts` and `failed_authorization` must be 0.

## 4. Release the kill switch

Every deploy writes a paused kill switch, so the review reports
`sending_paused=true` until this runs.

```bash
docker exec warmbly-confenge-backend-1 /app/confenge resume-sending
```

## 5. Live GO review

```bash
docker exec warmbly-confenge-backend-1 /app/confenge cohort review \
  --id <AUTHORIZATION_ID> --actor <FOUNDER_UUID>
```

Proceed only on `release_verdict=GO_FOR_CONTROLLED_EMAIL_PILOT` with every check
PASS. UNKNOWN is not PASS. Then persist the human verdict:

```bash
docker exec warmbly-confenge-backend-1 /app/confenge cohort review \
  --id <AUTHORIZATION_ID> --actor <FOUNDER_UUID> \
  --verdict READY_FOR_CONTROLLED_EMAIL_GO_REVIEW \
  --reason "<founder reason>" --confirm
```

## 6. Dispatch

```bash
docker exec warmbly-confenge-backend-1 /app/confenge cohort dispatch \
  --id <AUTHORIZATION_ID> --actor <FOUNDER_UUID> --limit 50    # preview

docker exec warmbly-confenge-backend-1 /app/confenge cohort dispatch \
  --id <AUTHORIZATION_ID> --actor <FOUNDER_UUID> --limit 50 --confirm
```

Enrollment queues campaign execution. Provider acceptance is reported by the
consumer after the worker reports transport success, so
`N_PROVIDER_ACCEPTED` lags `N_ATTEMPTED`. Do not infer delivery.

## 7. Kill switch

Stops enroll and send paths immediately, including anything already queued,
because the worker reads the same file switch at the SMTP boundary.

```bash
docker exec warmbly-confenge-backend-1 /app/confenge stop-sending
```

## 8. Observe

```bash
docker exec warmbly-confenge-backend-1 /app/confenge cohort report --events <PATH>
```

Record attempted, provider-accepted, bounce, reply, opt-out per route class.
Anything not yet observable stays UNKNOWN.

## Rollback

Images are tagged by SHA on every deploy.

```bash
cd /opt/warmbly-confenge
git reset --hard <PREVIOUS_SHA>
sed -i "s|^CONFENGE_REPOSITORY_SHA=.*|CONFENGE_REPOSITORY_SHA=<PREVIOUS_SHA>|" deploy/confenge-vps/.env
export COMPOSE_PROJECT_NAME=warmbly-confenge
for s in backend consumer worker; do
  docker tag warmbly-confenge-$s:<PREVIOUS_SHA> warmbly-confenge-$s:latest
done
bash deploy/confenge-vps/up.sh
```

Migrations 109 and 110 are additive and idempotent. Both have applicable down
migrations that clear rows the narrower constraints cannot satisfy before
restoring them. Revoke a grant instead of rolling back code when the concern is
the cohort rather than the release:

```bash
docker exec warmbly-confenge-backend-1 /app/confenge cohort-auth revoke --id <AUTHORIZATION_ID> --actor <FOUNDER_UUID>
```
