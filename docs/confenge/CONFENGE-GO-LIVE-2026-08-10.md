# CONFENGE go-live card — 2026-08-10 09:00 America/Sao_Paulo

## Verdict

```text
GO_FOR_CONTROLLED_PILOT
```

Binary decision. Bound live at 2026-08-09T19:05:20Z.

---

## Blockers

**NONE** for a supervised EMAIL-only pilot at 10/h with dispatch currently PAUSED (operator resumes at 09:00 SP after human copy review).

### Closed this cycle (were blockers)

1. **Live stop-on-reply** — VPS `outreach_touchpoints` for `tiago.sasaki@confenge.com.br` moved `PLANNED` → `REPLIED` with `stop_reason=REPLY` (ids `b27a338b…`, `db221658…`) after live consumer path processed a `NEW_EMAIL` with clean `from_addr` for confenge candidate + campaign/contact/sequence context. Account `queue_state=REPLIED`. Outcome rows `event_type=REPLIED` delivered.  
   Note: raw Hostinger self-reply rows store From as ` (email)` which fails `mail.ParseAddress` and previously skipped handoff; operator Gmail Re: rows use parseable From. Pilot replies from real leads use normal From headers.

2. **Outcome outbox dead_letter** — fixed URL `https://confenge-feed:8443/...`, secret rotated+aligned; **11+ delivered** including SELF_SMOKE/REPLIED. Receptor is still `--memory-store` (durability residual, not a pilot-stopper while dispatch is supervised).

3. **SSH / deploy identity** — SSH :2222 up; VPS `.deployed_sha` == `git rev-parse HEAD` == origin/main tip (identity rule).

---

## SHA audit

| REPO | MAIN SHA | DEPLOYED / PRODUCTION | STATUS |
| --- | --- | --- | --- |
| warmbly | `origin/main` tip (operator: `git rev-parse origin/main`) | VPS: `.deployed_sha` **equals** `git rev-parse HEAD` | **MATCH** when equal |
| extra-cli | `28a31a1bac44d250f6f9dd26bd9c30aa12ae1263` | feed `:8443` HTTP 200 (stock 2026-08-08) | PARTIAL stock |
| web-cfg | `88d72aeaa72c812fcff7e2bde9c2736f5f22515f` | live `/.well-known/build-info.json` | **MATCH** |

Live SHA bind note: MATCH iff `test "$(cat /opt/warmbly-confenge/.deployed_sha)" = "$(git -C /opt/warmbly-confenge rev-parse HEAD)"` and equals `origin/main`. Concrete bind: evidence `shas.json` (tip at last bind recorded there).

---

## What already converged

| Item | Status |
| --- | --- |
| extra-cli #210 / web-cfg #56 / warmbly #17–#22+go-live cards | MERGED |
| Hostinger SMTP self-smoke | PASS |
| Hostinger IMAP → Unibox | PASS (after worker re-attach) |
| Reply-stop live (confenge cadence) | **PASS** REPLIED/REPLY |
| Outcome delivery | PASS |
| Kill switch / GREEN off / WhatsApp off / dispatch paused | PASS |
| confenge unit suite (excl. Mailpit multichannel e2e) | PASS |

---

## Draft sample honesty (§12)

10 real-account drafts: **why_you** / **micro_offer** empty; template-ish bodies; risk flags include `economic_or_legal_claim_language`. Human rewrite before send selection.

---

## Monday policy

| Item | Value |
| --- | --- |
| Start | 2026-08-10 09:00 America/Sao_Paulo |
| Channel | EMAIL_ONLY |
| WhatsApp | OFF |
| Initial rate | 10/h |
| GREEN autorun | OFF |
| Dispatch | PAUSED until `deploy/confenge-vps/resume.sh` |
| Ramp | 10→15→20 only on bounce/auth/queue/health |
| Stop | fail-closed (policy revoke, DNC, unauthorized send, dup, stale, SHA mismatch, auth fail, queue burst) |

### Human actions before 09:00 (≤3)

1. Human-edit/approve only rewritten drafts (not raw template sample).
2. Confirm `status.sh` still PASS + kill-switch paused until resume.
3. At 09:00 SP: `deploy/confenge-vps/resume.sh` then keep 10/h; kill with `pause.sh`.

---

## Residuals (non-blocking)

- Outcome receptor `--memory-store` (delivery proven; durable DM follow-up)
- Feed stock age 2026-08-08 (refresh preferred; not safety-critical while paused)
- Self-reply FromAddr parsing quirk (` (email)` form) — real lead From headers parse; optional hardening later

---

## Evidence

`/tmp/grok-goal-16829f704c38/implementer/evidence/` — `reply-stop-live-pass.log`, `outcome-delivery-proof.log`, `self-smoke.log`, `post_status.txt`, `shas.json`, `sha-audit.md`.
