# CONFENGE VPS execution plane

Isolated always-on Warmbly deployment for CONFENGE on Netcup, co-tenant with
extra-cli but **not** sharing application state.

Go images build with `GO_TAGS=minprofile`: postgres, redis, nats, filesystem
blobs, and local KMS only. Stripe, AWS, GCP Cloud Tasks/PubSub, and Kafka are
not compile-linked and fail closed if selected.

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

Safety: GREEN autorun OFF, auto-send OFF, WhatsApp OFF, human approval ON.
Operational pace 10→20/h, daily shell 200. Hostinger plan is **Business Email
Starter** (1000 msgs/rolling 24h/mailbox); that is only the provider ceiling,
not the commercial target (`HOSTINGER_PLAN_CLASS=BUSINESS_EMAIL_STARTER`).
