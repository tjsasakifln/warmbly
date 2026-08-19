# Evidence — CONFENGE-WARMBLY-SPEED-TO-LEAD-01

Redacted. No phone, email, person name, CNPJ, or secret values.

## Verdict

`DEPLOYED_COCKPIT_READY_INTERNAL_TRANSPORT_BLOCKED`

Durable operator alerts are live on the CONFENGE VPS. Cockpit, badge, ack, and first-action work. Optional internal email remains honestly blocked (no isolated transporter; campaign SMTP is not used). Issue #47 stays OPEN.

## Baseline

| Item | Value |
| --- | --- |
| origin/main before campaign | `d0ce23e82fdff763f57674f4a133548a229fe80a` (#94) |
| PR #93 | `MERGE_NOW` squash `f0605d5915eedc76a82ce7cfd7035755593c695e` |
| PR #95 | merged `c1afd280839ad7446997e16f6ab059fbd3c3ee56` |
| PR #96 ack actor hotfix | merged `63b866df86431460dbb8cc07249d69c6188aab50` |
| MAIN_SHA_AFTER | `63b866df86431460dbb8cc07249d69c6188aab50` |
| DEPLOY_SHA | `63b866df86431460dbb8cc07249d69c6188aab50` |
| backend image | `sha256:7d174112deb74336130df1c6d9c4712b679d833e56929c2c14035009cd01b3fc` |
| rollback backup | `/opt/warmbly-confenge/data/backups/confenge-vps/warmbly-confenge-20260819T225314Z.tar.gz` |
| profile | `GO_TAGS=minprofile` |
| loopback `/live` | 200 live |
| loopback `/ready` | 200 all required planes ok |
| loopback `/health` | 200 ok |
| inbound health public+loopback | 200 READY, `auto_send_enabled=false`, `dispatch_attempted=false` |
| kill switch | paused / `reason=deploy_preflight` |

## Reused capabilities

HMAC inbound webhook, persist-first `outreach_inbound_leads`, lead_id replay identity, secondary identity dedupe, `InboundCommercialSkipReason`, INBOUND NOW projector, exception queue, scoreboard `include_synthetic=0`, `RecordInboundOutcome`, `confenge.commercial_event.v1`, canonical host `api.confenge.com.br`, `CONFENGE_AUTO_SEND_ENABLED=false`.

## Residual implemented

- Additive `outreach_operator_alerts` (migration `000105`)
- One logical alert per org/lead/event_id
- Aging bands NEW / ATTENTION / AGED / ACKNOWLEDGED / ACTION_RECORDED / RESOLVED_NO_ACTION / ALERT_FAILED (not SLA)
- Ingest hook after receipt commit; alert-store failure holds `alert_store_failed`
- Acknowledge + resolve-no-action; first action via existing outcome registrar
- INBOUND NOW badge, urgency sort, recebido há, owner, Reconhecer, real/synthetic filter
- Browser notification when granted; permission-denied fallback
- Optional operator email default-off, kill-switched, allowlisted, PII-free, never lead-derived, never campaign SMTP. Honest block: `blocked_no_isolated_transport`

## Synthetic canary (labeled, not a real lead)

Loopback HMAC `POST /api/v1/webhooks/confenge/inbound`:

| Step | Result |
| --- | --- |
| unsigned | HTTP 401 invalid inbound signature |
| `t=1,v1=deadbeef` | HTTP 401 |
| signed SYNTHETIC `infrastructure_canary` | HTTP 201, `dispatch_attempted=false` |
| replay | HTTP 200 duplicate, same row |
| default INBOUND NOW | count 0, canary absent, unacked real 0 |
| `include_synthetic=1` | canary present, `synthetic=true`, dispatchable false |
| acknowledge | HTTP 200 `ACKNOWLEDGED`, actor set, replay HTTP 200 |
| first action `ATTEMPTED` | HTTP 200, not claimed as commercial outcome |

Canary lead_id class: `synthetic-speed-to-lead-01-*`. Not posted via public form.

## Events

`operator_alert_created`, `operator_alert_emitted`, `operator_alert_failed`, `operator_alert_acknowledged`, `first_human_action_recorded`, `inbound_resolved_no_action`. Not reply / QCO / meeting / proposal / WON / LOST / pipeline / revenue.

## Latency

Stamps are observable. Real percentiles UNKNOWN. No SLA.

## Flags / recipient policy

- `CONFENGE_AUTO_SEND_ENABLED=false`
- `CONFENGE_OPERATOR_ALERT_EMAIL_ENABLED=false`
- `CONFENGE_OPERATOR_ALERT_EMAIL_KILL_SWITCH=true`
- Recipient is allowlisted `CONFENGE_OPERATOR_ALERT_EMAIL` only. Never lead-derived. No CC/BCC. Generic subject. No PII.

## LEAD_CONTACTED

no

## Rollback

`000105` down.sql drops the alert table and restores the 000104 exception CHECK. Redeploy previous SHA from the backup archive.
