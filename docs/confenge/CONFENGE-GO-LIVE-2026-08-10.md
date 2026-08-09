# CONFENGE go-live card — 2026-08-10 09:00 America/Sao_Paulo

## Verdict

```text
GO_FOR_CONTROLLED_PILOT
```

Binary decision for supervised commercial pilot start. Not unrestricted autorun.

Emitted 2026-08-09 after cross-repo merge, Netcup deploy to `main`, full host reboot, Hostinger self-smoke, feed sync, 10 real no-send drafts, and outcome HMAC roundtrip.

---

## SHA audit

| REPO | MAIN SHA | DEPLOYED SHA | STATUS |
| --- | --- | --- | --- |
| extra-cli | `28a31a1bac44d250f6f9dd26bd9c30aa12ae1263` | VPS `/opt/extra-consultoria` (intelligence plane; organic engine merged) | MATCH (PR #210 merged) |
| warmbly | `b2bebda08482ce820927ee122c3de331168a5f72` | VPS `/opt/warmbly-confenge` `.deployed_sha` + `git HEAD` | MATCH |
| web-cfg | `c550e7cc7d9486b5095df66d8b3baa97c588eabd` | Netlify production (main push site-ci green; site 200) | MATCH (PR #56 merged) |

Warmbly intermediate: #18 doctrine `55ecb0c9` then #17 VPS pack `b2bebda0` on top of hardening main.

---

## Merges completed (convergence order)

1. **extra-cli #210** Organic Opportunity Engine (squash) — does not touch `confenge_activation` / hot set / send readiness
2. **web-cfg #56** inbound opportunity engine + pilot cohort (squash) — required checks site-ci + pSEO green
3. **warmbly #18** evidence-led outreach doctrine + learning loop (squash)
4. **warmbly #17** Netcup always-on execution plane, rebased on post-#18 main (clean rebase; portable `mktemp` fix in `validate.sh`) then squash-merged

---

## Deploy inventory (Netcup)

| Field | Value |
| --- | --- |
| deployed_repo | `tjsasakifln/warmbly` |
| deployed_branch | `main` (detached HEAD at merge commit) |
| deployed_sha | `b2bebda08482ce820927ee122c3de331168a5f72` |
| deployed_at | 2026-08-09T16:43:00Z (build+up); post-reboot 2026-08-09T16:46:40Z |
| host | `159.195.18.88` / `v2202607385716487230` |
| project | `warmbly-confenge` under `/opt/warmbly-confenge` |
| compose | `docker-compose.yml` + `deploy/confenge-vps/docker-compose.override.yml` |
| schema | migrations through **91** |
| mailbox | `tiago.sasaki@confenge.com.br` active (`smtp_imap`) |
| feed plane | `/opt/confenge-plane` :8443 + outcome receptor :8790 |

Containers post-reboot: backend/worker/consumer/web/postgres/redis/nats/tracking/realtime/mailpit + confenge-plane-feed. Restart policies keep stack up after full host reboot.

### Go-live ops fix (feed DNS/TLS)

Feed container is `network_mode: host` with cert CN `confenge-feed`. Backend needs:

- `extra_hosts: confenge-feed:host-gateway` (baked into override on VPS + this pack)
- env URLs `https://confenge-feed:8443/...` (not bare `host.docker.internal`, which fails TLS name check)

Verified: `POST /confenge/sync` → `200` `skipped_same_snapshot` for 168-lead feed.

---

## Safety posture (must hold through Monday open)

| Control | State |
| --- | --- |
| Kill switch file `/data/confenge-ops/kill-switch` | **paused** |
| `CONFENGE_SENDING_PAUSED` | **true** (until operator resume Monday) |
| `CONFENGE_GREEN_AUTORUN_ENABLED` | **false** |
| `CONFENGE_AUTO_SEND_ENABLED` | **false** |
| `CONFENGE_REQUIRE_HUMAN_APPROVAL` | **true** |
| `CONFENGE_WHATSAPP_ENABLED` | **false** |
| Mode | `EMAIL_ONLY` |
| Rate | adaptive start **10/h**, max **20/h** |
| Window | 09:00–18:00 `America/Sao_Paulo`, business days only |
| Campaign daily shell | 200 (governor + window → effective ~90 theoretical slots/day at 10/h) |

Do **not** enable unrestricted GREEN autorun for day-1. Pre-authorize only within already-approved policy path after human review.

---

## Monday pilot policy (explicit)

| Item | Value |
| --- | --- |
| Start | **2026-08-10 09:00 America/Sao_Paulo** |
| Channel | EMAIL_ONLY (WhatsApp OFF) |
| Initial rate | **10 sends/hour** |
| Progression | metrics-gated only (see ramp) |
| Approval | human review of drafts; no mass auto-approve |
| Resume path | clear kill switch + `deploy/confenge-vps/resume.sh` (and set `CONFENGE_SENDING_PAUSED=false` only when ready) |
| Pause path | `deploy/confenge-vps/pause.sh` |

### Ramp gate (10 → 15 → 20 /h)

**Stay at 10/h if any of:** elevated bounce, provider failures, spam complaints, mailbox auth failure, queue anomaly, contact precision concern, unexpected DNC/unsubscribe, mailbox health degradation, safety violation.

**Consider 15/h only after:** minimum sample of successful supervised sends, bounce acceptable, mailbox stable, zero safety violations, operator review OK.

**Consider 20/h only after:** second stable window under 15/h with same gates.

Do **not** use open rate as primary ramp gate.

### Stop conditions (fail closed)

Immediate outbound stop if:

- CAMPAIGN_POLICY invalid/revoked
- mailbox auth failure
- provider ban/warning
- DNC leak
- send without approval/policy
- duplicate sends
- stale context bypass
- unexpected queue burst
- bounce above operational limit
- missing ownership / contact integrity regression
- deployment SHA mismatch vs this card
- outcome path corrupting commercial state

---

## Proof matrix (2026-08-09)

| Gate | Result | Evidence |
| --- | --- | --- |
| PRs converged | PASS | #210, #56, #18, #17 MERGED |
| Warmbly main = VPS SHA | PASS | `b2bebda0…` both |
| Migrations coherent | PASS | schema version **91** after deploy |
| Hostinger SMTP | PASS | TCP+AUTH; worker `Email sent successfully` |
| Hostinger IMAP | PASS | status + prove-hostinger-net |
| Self-smoke (operator mailbox only) | PASS | task `02196ba2-…` → Hostinger send OK; subject `CONFENGE SELF_SMOKE 20260809T164501Z` |
| Kill switch | PASS | file pause survives reboot |
| Governor / dispatch paused | PASS | status DISPATCH PAUSED; sending_allowed false |
| Full VPS reboot | PASS | host rebooted; docker auto-start; data 332 accounts retained; no send burst |
| Feed import/sync | PASS | 168-lead stock; sync 200 noop same snapshot |
| Real drafts (no send) | PASS | 10× `NEEDS_REVIEW` drafts written; sample `/root/golive-draft-sample.json` on VPS |
| Outcome HMAC roundtrip | PASS | CONTACTED create + duplicate replay; auto-WON 422 rejected |
| WhatsApp | OFF | env + status |
| GREEN autorun | OFF | env + status |

### Observability snapshot (pre-Monday)

| Metric | Value |
| --- | --- |
| Imported accounts | 332 |
| Ready to generate | 167 |
| Needs contact | 163 |
| Needs review (drafts) | generated sample 10; product queue was 0 before sample |
| Approved / enrolled | 0 / 2 (prior enrollments) |
| Sent / bounced / replied / DNC | 0 / 0 / 0 / 0 (commercial) |
| Feed age at last check | ~16h (refresh before Monday open) |
| Outcome loop | ready (receptor active) |
| Kill switch | on |

---

## Human review packs (Sunday focus)

### A. Contact sample

Use existing extra-cli contact-enrichment pilot artifacts (accepted / rejected / unresolved). Do not expand enrichment scope before pilot.

### B. Copy sample (10 real drafts)

Generated on VPS against real EMAIL_SEND_READY accounts. All `NEEDS_REVIEW`. Typical risk flags seen:

- `economic_or_legal_claim_language`
- `strategy_compose`
- `evidence_requires_hypothesis_language`

Subjects are still template-leaning (`Contrato <short name>`). Bodies use public-portfolio framing and avoid treating annualidade as automatic credit. **Human must edit/approve before any lead send.** Full JSON: VPS `/root/golive-draft-sample.json`.

### C. This go-live card

Code, mailbox, safety, volume, first-day policy, kill switch, blockers.

---

## Operator open checklist (Monday 08:45–09:00)

1. Confirm `git -C /opt/warmbly-confenge rev-parse HEAD` == `b2bebda08482ce820927ee122c3de331168a5f72` (or later intentional main only)
2. `bash deploy/confenge-vps/status.sh` all PASS; GREEN OFF; WhatsApp OFF
3. Refresh extra-cli feed if age > 24h; `POST /confenge/sync`
4. Human-approve only reviewed drafts (content-hash path)
5. Clear kill switch + resume dispatch **only when first approved batch is ready**
6. Keep rate at 10/h; watch bounce/auth/queue for first hour
7. Pause immediately on any stop condition

---

## Known non-blockers

- Draft copy is commercially usable only after human edit (risk flags expected under doctrine QA)
- Consumer may log `no handler registered for EMAIL_SENT` on generic account send path; self-smoke SMTP still PASS
- Outcome receptor uses `--memory-store` (not durable Decision Memory DB); HMAC/idempotency/WON policy still proven
- web-cfg CodeQL job failed historically on #56; not a required branch check; site-ci/pSEO green
- Inbound SEO ranking not required for outbound pilot

---

## Blockers

**None** that prevent a supervised EMAIL_ONLY pilot at 10/h with human approval and kill switch.

---

## Non-goals (do not open during pilot week)

Stakeholder Graph, Revenue Conversion OS, WhatsApp cold, mass pSEO publication, unrestricted autorun, 50+/h, multi-mailbox scale.
