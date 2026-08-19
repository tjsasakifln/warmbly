# Evidence — CONFENGE-WARMBLY-SPEED-TO-LEAD-01

Redacted. No phone, email, person name, CNPJ, or secret values.

## Verdict

`BLOCKED_WITH_NAMED_RESIDUAL` until this SHA is merged and promoted on the CONFENGE VPS. Code and tests for durable operator alerts are in this PR. Internal email transport is honestly blocked (no isolated transporter; campaign SMTP is not used). Issue #47 stays OPEN.

## Baseline

| Item | Value |
| --- | --- |
| origin/main before campaign | `d0ce23e82fdff763f57674f4a133548a229fe80a` (#94) |
| PR #93 | `MERGE_NOW` squash `f0605d5915eedc76a82ce7cfd7035755593c695e` |
| origin/main at branch | `f0605d5915eedc76a82ce7cfd7035755593c695e` |
| live git HEAD on VPS | `dd4490825f01350add510f41746500947d83f850` (#92) |
| `.deployed_sha` on VPS | `deabc11e715d508a68ee231376148b52ee4b8aca` |
| public inbound health | 200 READY, `auto_send_enabled=false`, `dispatch_attempted=false` |
| loopback `/live` `/ready` `/health` | 200 live / ready / ok (pre-promote of this PR) |

## Reused capabilities

HMAC inbound webhook, persist-first `outreach_inbound_leads`, lead_id replay identity, secondary identity dedupe, `InboundCommercialSkipReason` (synthetic/qa/internal), INBOUND NOW projector, exception queue (owner/reason/next), scoreboard `include_synthetic=0`, `RecordInboundOutcome` / human-outcome registrar, `confenge.commercial_event.v1`, canonical host `api.confenge.com.br`, `CONFENGE_AUTO_SEND_ENABLED=false`.

## Residual implemented

- Additive table `outreach_operator_alerts` (migration `000105`)
- One logical alert per org/lead/event_id (`inbound_operator_attention:{lead_id}`)
- Aging bands NEW / ATTENTION / AGED / ACKNOWLEDGED / ACTION_RECORDED / RESOLVED_NO_ACTION / ALERT_FAILED (not SLA)
- Ingest hook after receipt commit; alert-store failure holds `alert_store_failed` without deleting the receipt
- `POST /confenge/inbound/:leadId/acknowledge` (actor, idempotent)
- `POST /confenge/inbound/:leadId/resolve` (actor + reason; refuses WON/LOST/revenue)
- First action via existing inbound outcome registrar; stamps alert first_action
- INBOUND NOW badge, sort by urgency, band highlight, recebido há, owner, Reconhecer, resolve-no-action, real/synthetic filter
- Browser Notification when granted; permission-denied visual/sound fallback
- Optional operator email: allowlisted, default-off, kill switch default on, generic subject, no PII, never lead-derived recipient, never campaign SMTP. Honest block: `blocked_no_isolated_transport`

## Events (additive on existing ledger)

`operator_alert_created`, `operator_alert_emitted`, `operator_alert_failed`, `operator_alert_acknowledged`, `first_human_action_recorded`, `inbound_resolved_no_action`.

These are not reply, qualified conversation, meeting, proposal, WON/LOST, pipeline, or revenue.

## Latency (observable, no SLA)

lead persisted → alert durable; alert durable → first emitted; lead persisted → acknowledged; acknowledged → first human action; lead persisted → first human action. Open/censored cycles stay visible. Real percentiles UNKNOWN (no representative real sample).

## Flags

- `CONFENGE_AUTO_SEND_ENABLED=false`
- `CONFENGE_OPERATOR_ALERT_EMAIL_ENABLED=false` (default)
- `CONFENGE_OPERATOR_ALERT_EMAIL_KILL_SWITCH=true` (default)
- `CONFENGE_OPERATOR_ALERT_EMAIL` allowlist only; empty until a dedicated transporter exists

## Alert recipient policy

Exactly one configured internal address. Never copied from the lead. No CC/BCC. Subject `Novo lead real no INBOUND NOW`. Body is timestamp, non-sensitive origin/asset, age, and panel link. Campaign/outreach send path is not used.

## Tests

Shipped Go tests drive `IngestInboundLead` plus ack/resolve/aging/email policy. Two runs in `{SCRATCH}` logs. Web: operatorNotify permission-denied fallback unit plus labels.

## LEAD_CONTACTED

no

## AUTO_SEND

false

## Rollback

`000105_outreach_operator_alerts.down.sql` drops the alert table and restores the 000104 exception CHECK. Feature is additive; inbound persist path remains if alerts are disabled.
