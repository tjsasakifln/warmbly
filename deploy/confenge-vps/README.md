# CONFENGE VPS execution plane

Isolated always-on Warmbly deployment for CONFENGE on Netcup, co-tenant with
extra-cli but **not** sharing application state.

Go images build with `GO_TAGS=minprofile`: postgres, redis, nats, filesystem
blobs, and local KMS only. Stripe, AWS, GCP Cloud Tasks/PubSub, and Kafka are
not compile-linked and fail closed if selected.

## Deployment model

Production consumes immutable images that CI already built. The VPS does not
compile. `deploy/confenge-vps/docker-compose.release.yml` pins every application
service to `ghcr.io/tjsasakifln/warmbly/<service>:<release-sha>` (the Go services
to the `-minprofile` variant) and resets each `build:` section, so a missing
image is a loud failure instead of a silent local build.

```bash
deploy/confenge-vps/release-deploy.sh            # roll onto origin/main
deploy/confenge-vps/release-deploy.sh <full-sha> # roll onto a specific release
```

`up.sh` runs the deploy in this order and stops at the first failure:

1. disk preflight, before any mutation
2. bounded cleanup of reconstructible artifacts if headroom is short
3. pull the images pinned to the release SHA
4. verify each image carries `org.opencontainers.image.revision=<sha>`
5. write the `deploy_preflight` kill switch (outbound cannot fail open)
6. `compose up -d --no-build --remove-orphans`
7. backend health, `pg_isready`, and `verify-release.sh` per service
8. clear the deploy kill switch automatically
9. bounded retention sweep

Step 8 clears only a switch whose reason is `deploy_preflight`. An operator
pause from `pause.sh` has a different reason and survives a deploy. After a
successful deploy the business send window is the only outbound gate, at any
hour.

Set `CONFENGE_RELEASE_MODE=build` only if GHCR is unreachable. Local production
builds are what filled the root filesystem.

Rollback pulls the previous release the same way. Releases published before the
`-minprofile` variant existed need `CONFENGE_GO_IMAGE_SUFFIX=` to select the
plain tag; the local `warmbly-confenge-*` images from the pre-registry era are
left in place as the rollback path for those releases and are never pruned by
the retention sweep.

## Disk safety

`data/backups` reached 4.3 GB inside a 4.7 GB Docker build context, every
rebuild snapshotted it, builder cache grew to ~174 GB, and Postgres could no
longer extend files. Three things prevent a repeat:

- `.dockerignore` excludes `data`, `ops`, `ops-evidence`, `backups`, archives
  and dependency trees. `scripts/build_context_size.py` is the CI gate (150 MB
  budget); no Dockerfile copies any excluded tree.
- `disk-guard.sh preflight` refuses a deploy below `20 GB` deploy budget plus a
  `20 GB` Postgres reserve (and 12% free), after trying a bounded reclaim of
  reconstructible artifacts only.
- `disk-guard.sh retain` runs after every deploy and daily via
  `confenge-docker-gc.timer`: builder cache capped at 8 GB / 168 h, dangling
  images pruned, Warmbly release images older than the current and previous
  release removed without `-f`. Named volumes, Postgres data, `confenge_ops`,
  `confenge_keys` and `blobs` are never touched.

```bash
deploy/confenge-vps/disk-guard.sh report      # thresholds and current usage
deploy/confenge-vps/host-disk-report.sh       # shared-host picture, read-only
sudo deploy/confenge-vps/docker-gc-install.sh # timer + host BuildKit cache cap
```

Backups keep the newest `CONFENGE_BACKUP_KEEP` (default 10) archives with their
manifests, checksums and key bundles. Co-tenant data (Extra Consultoria, the
control center, host Postgres) is reported and never deleted by Warmbly
automation.

Full docs:

- [vps-execution-plane.md](../../docs/confenge/vps-execution-plane.md)
- [vps-execution-inventory.md](../../docs/confenge/vps-execution-inventory.md)
- [vps-disaster-recovery.md](../../docs/confenge/vps-disaster-recovery.md)

Quick start (on VPS checkout at `/opt/warmbly-confenge`):

```bash
deploy/confenge-vps/install.sh
deploy/confenge-vps/prove-hostinger-net.sh   # SMTP must pass (Netcup unlock if not)
deploy/confenge-vps/up.sh
# After Netcup unlocks outbound 465/587:
#   CONFENGE_SELF_SMOKE_TO=<you> deploy/confenge-vps/post-smtp-unlock.sh
deploy/confenge-vps/connect-hostinger.sh     # interactive password; seals in DB
deploy/confenge-vps/status.sh
```

Operator browser (from laptop):

```bash
deploy/confenge-vps/tunnel.sh
# http://127.0.0.1:5173
```

The page opens directly in PT-BR without interactive authentication. The backend
still issues an organization-scoped session to the configured technical operator.
Keep the UI and API on loopback and access them only through the SSH tunnel.
The helper keeps local port `8080` available for Evolution API and forwards the
CONFENGE backend to `18080`. Use `deploy/confenge-vps/compose.sh` for maintenance;
raw `docker compose` commands do not load this deployment's override and `.env`.

### Asaas transport adapter

The first install creates `/etc/confenge/asaas-adapter.env` with mode `0600`
and exits before starting the service. Fill the Asaas auth token, the scoped
Warmbly bearer token and the internal webhook secret, then run:

```bash
sudo deploy/confenge-vps/asaas-adapter-install.sh
curl -fsS http://127.0.0.1:8791/api/v1/webhooks/asaas/health
sudo python3 deploy/confenge-vps/asaas-adapter/adapter.py permissions
```

The Asaas token must contain 32 to 255 non-whitespace characters and must not be
an Asaas API key. Startup validates all secrets, retry bounds, loopback binding
and the exact Warmbly destination before opening the listener. Dry-run retains
events as `blocked`; it never labels an unforwarded event `processed`.

To update, check out the reviewed SHA and rerun the install script. It prints
`ASAAS_ADAPTER_SHA` after restart. To roll back, restore the matching checkout,
rerun the same script and verify health plus queue permissions. Back up the
SQLite queue before changing adapter schema versions.

Public inbound edge (host nginx, not a new app):

```bash
# as root, after DNS A api.confenge.com.br -> 159.195.18.88 (DNS only)
deploy/confenge-vps/inbound-edge-install.sh
```

Only `GET .../inbound/health`, `POST .../inbound`,
`GET .../asaas/health`, and `POST .../asaas` are public. The Asaas route
terminates on the versioned persist-first adapter at loopback `:8791`; it does
not create customers, checkouts, subscriptions, or charges. Handoff:
[inbound-edge.md](../../docs/confenge/inbound-edge.md).

Safety: GREEN autorun OFF, auto-send OFF, WhatsApp OFF and the global human
fallback gate ON. An eligible first touch may instead use the explicit
`CFG-FIRST-TOUCH-ROUTING-v1` delegated path; every exception remains human.
Operational pace 10→20/h, daily shell 200. Hostinger plan is **Business Email
Starter** (1000 msgs/rolling 24h/mailbox); that is only the provider ceiling,
not the commercial target (`HOSTINGER_PLAN_CLASS=BUSINESS_EMAIL_STARTER`).
