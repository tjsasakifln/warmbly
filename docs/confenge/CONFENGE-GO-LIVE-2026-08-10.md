# CONFENGE go-live card — 2026-08-10 09:00 America/Sao_Paulo

## Verdict

```text
NO_GO
```

Binary decision. Not mostly ready. Not a soft green.

Emitted 2026-08-09T18:45Z after live rebind, outcome outbox repair, IMAP re-attach, and reply-stop re-attempt.

---

## Blockers (only pilot-stoppers)

1. **Live confenge cadence stop-on-reply still unclosed.**  
   Proven on the live VPS path:
   - Hostinger SMTP self-smoke (`66c3325f…` Email sent successfully)
   - IMAP auth + Unibox ingest of `CONFENGE SELF_SMOKE 20260809T181500Z` / `T182230Z`
   - Unibox ingest of operator replies `Re: CONFENGE SELF_SMOKE 20260809T181500Z` and `Re: …T182230Z` (including `REPLY_STOP_PROOF2`)
   - Seeded confenge account/candidate `tiago.sasaki@confenge.com.br` with 2 `PLANNED` touchpoints  
   **Not proven:** those touchpoints never left `PLANNED` after Unibox had the Re: rows; consumer showed no confenge handoff/EMAIL_REPLIED cancel. Unit suite still PASS (`TestS22_ReplyStopsCadence`, `ProcessInboundHandoff*`).  
   Until `PLANNED` → stopped (`stop_reason` reply/DNC) is observed on a live ingest, Monday fail-closed reply handling is not production-proven.

2. **(Cleared this cycle) Outcome Warmbly → receptor delivery.**  
   Was: 10/10 `dead_letter` via `host.docker.internal` TLS SAN mismatch + secret file/env mismatch.  
   Now: secret rotated + aligned; URL `https://confenge-feed:8443/webhooks/warmbly/outcome`; **11/11 delivered** including `test:self_smoke:outcome:…`. Receptor remains `--memory-store` (durable Decision Memory still not the live path; call that residual, not a silent PASS for §14 durability).

Clear (1) with one observed cadence cancel after Unibox ingest (or explicit operator exception). Then re-emit GO.

---

## SHA audit

| REPO | MAIN SHA | DEPLOYED / PRODUCTION | STATUS |
| --- | --- | --- | --- |
| extra-cli | `28a31a1bac44d250f6f9dd26bd9c30aa12ae1263` | feed `:8443` HTTP 200 (stock generated_at 2026-08-08) | PARTIAL stock |
| warmbly | `5841ebc45b6e5ae50458410e688482c1e29c7d1f` | VPS `.deployed_sha` + `git HEAD` same; `status.sh` PASS | **MATCH** |
| web-cfg | `88d72aeaa72c812fcff7e2bde9c2736f5f22515f` | live `/.well-known/build-info.json` commit match | **MATCH** |

---

## What already converged (keep; do not redo)

| Item | Status |
| --- | --- |
| extra-cli #210 / web-cfg #56 / warmbly #17–#22 lineage | MERGED on mains |
| VPS SSH `:2222` | UP; `sshd` enabled |
| Full reboot drill | EXECUTED earlier PASS; sshd recovered via SCP after that reboot |
| Hostinger SMTP self-smoke | PASS live |
| Hostinger IMAP → Unibox | PASS after worker re-attach (`email account added to worker`) |
| Outcome outbox delivery | **PASS** 11 delivered (secret rotated) |
| Kill switch / GREEN off / WhatsApp off / dispatch paused | PASS |
| confenge unit suite (excl. Mailpit multichannel e2e) | PASS |

---

## Draft sample honesty (§12)

10 real-account drafts (`COPY-SAMPLE-2026-08-10.md`): **why_you** and **micro_offer** empty; bodies near-template; risk flags include `economic_or_legal_claim_language`. Human rewrite required before send selection. Not a silent safety hole.

---

## Monday policy (when GO is re-earned)

| Item | Value |
| --- | --- |
| Start | 2026-08-10 09:00 America/Sao_Paulo |
| Channel | EMAIL_ONLY |
| WhatsApp | OFF |
| Initial rate | 10/h |
| GREEN autorun | OFF |
| Dispatch | PAUSED until explicit `resume.sh` |
| Ramp | 10→15→20 only on bounce/auth/queue/health (not open rate) |
| Stop | fail-closed on policy revoke, DNC leak, unauthorized send, dup send, stale context, SHA mismatch, auth failure, queue burst |

### Human actions before 09:00 (≤3)

1. Close reply-stop live: after a Re: lands in Unibox for a confenge candidate with open touchpoints, confirm states leave `PLANNED` with reply stop_reason (or accept NO_GO).
2. Human-edit/approve drafts only (not raw template sample).
3. Only then: `deploy/confenge-vps/resume.sh` at 09:00 SP; keep 10/h; kill with `pause.sh`.

---

## Non-blockers

- Empty why_you / micro_offer (rewrite queue)
- Feed stock age while dispatch paused
- Outcome receptor `--memory-store` (delivery proven; durable DM follow-up)
- Root password rotated during sshd recovery (env updated)

---

## Evidence

`/tmp/grok-goal-16829f704c38/implementer/evidence/` — `post_status.txt`, `deploy-warmbly.json`, `self-smoke.log`, `imap-inbox-proof.log`, `reply-stop*.log`, `outcome-delivery-proof.log`, `sha-audit.md`, `webcfg-deploy.json`.  
Secrets scrubbed from evidence; outcome webhook secret rotated on VPS.
