# CONFENGE go-live card — 2026-08-10 09:00 America/Sao_Paulo

## Verdict

```text
NO_GO
```

Binary decision. Not "mostly ready". Not a soft green.

Emitted 2026-08-09T17:25Z after re-audit of skeptic gaps. Convergence and earlier live proofs remain valuable, but **current operator control plane access is broken** and **IMAP reply stop was never closed on the live cycle**.

---

## Blockers (only real pilot-stoppers)

1. **VPS SSH down** (`159.195.18.88:2222` Connection refused at evidence bind). Host ping and feed/outcome `:8443` still respond, so the machine is up, but the operator cannot run status/pause/resume/self-smoke/draft review against Warmbly loopback APIs. Monday pilot is not operable without SSH or Netcup SCP VNC recovery of `sshd`.
2. **IMAP reply ingest + stop-on-reply** not proven on the converged live cycle. Self-smoke proved Hostinger **SMTP send** only (`task 02196ba2…` / `Email sent successfully`). Unit suite proves reply/DNC cancel futures in-process; that is **not** a live IMAP proof.
3. **Warmbly deploy identity re-verify blocked** by (1). Last SSH-verified MATCH was `9543f387…` at 16:55:40Z; cannot re-confirm `.deployed_sha` now. Objective requires exact current MATCH, not stale last-seen.

Clear these three, re-bind evidence, then re-emit GO.

---

## SHA audit

| REPO | MAIN SHA | DEPLOYED / PRODUCTION | STATUS |
| --- | --- | --- | --- |
| extra-cli | `28a31a1bac44d250f6f9dd26bd9c30aa12ae1263` | main includes organic engine; public feed `:8443` serves `confenge.outreach.v1` (168 leads) | MATCH (main); live process tree not re-inspected |
| warmbly | `eea32a9c9a4baf16b9f0734da620a9d0f4851094` | Last SSH: VPS `.deployed_sha`/`git HEAD`=`9543f387…` (16:55:40Z). **SSH refused since.** Tip includes #20 NO_GO + #21 recovery SOP | **STALE_PROOF** (re-verify blocked) |
| web-cfg | `c550e7cc7d9486b5095df66d8b3baa97c588eabd` | Netlify production + live `/.well-known/build-info.json` commit identical, published 2026-08-09T16:36:19Z | **MATCH** |

Warmbly chain: #18 doctrine → #17 VPS pack `b2bebda0` (images built here) → #19 go-live pack `9543f387` → #20 NO_GO rebind → #21 recovery SOP `eea32a9c`.

---

## What already converged (keep; do not redo)

| Item | Status |
| --- | --- |
| extra-cli #210 | MERGED |
| web-cfg #56 | MERGED + Netlify production MATCH |
| warmbly #18 | MERGED |
| warmbly #17 rebased on post-#18 main (clean) | MERGED |
| warmbly #19 go-live pack | MERGED |
| Full host reboot drill | EXECUTED PASS (while SSH worked) |
| Migrations | 91 at last verify |
| Hostinger SMTP self-smoke | PASS (historical live log) |
| Hostinger TCP IMAP | PASS at last status.sh |
| Feed sync | PASS (168 leads, same-snapshot noop) |
| Outcome HMAC create + replay + no auto-WON | PASS on live receptor (`--memory-store`) |
| Kill switch / GREEN off / WhatsApp off / dispatch paused | PASS at last verify |
| confenge unit/contract suite (excl. Mailpit e2e) | PASS on `9543f387` |
| Authority: organic ≠ activation | PASS |

---

## Draft sample honesty (§12)

10 real-account drafts generated as `NEEDS_REVIEW` (see `COPY-SAMPLE-2026-08-10.md`).

| Field | Observation |
| --- | --- |
| Real companies / CNPJ / contacts | Yes |
| Strategy-driven **why_you** | **Empty** in API payload |
| **micro_offer** | **Empty** |
| why_now | Populated from activation moment summary (often portfolio boilerplate) |
| Bodies | Near-template; doctrine risk flags include `economic_or_legal_claim_language` |
| Commercial usability | **Not** send-ready without human rewrite |

Do not claim a full strategy/doctrine package for Monday send selection until drafts are regenerated or heavily edited by a human.

---

## Monday policy (frozen for when GO is re-earned)

| Item | Value |
| --- | --- |
| Start | 2026-08-10 09:00 America/Sao_Paulo |
| Channel | EMAIL_ONLY |
| WhatsApp | OFF |
| Initial rate | 10/h |
| GREEN autorun | OFF |
| Human approval | required |
| Ramp | 10→15→20 only on bounce/auth/queue/health metrics (not open rate, not clock) |
| Stop | fail-closed on policy revoke, DNC leak, unauthorized send, dup send, stale context, SHA mismatch, auth failure, queue burst |

### Human actions before 09:00 (≤3)

1. **Restore VPS SSH** (Netcup SCP VNC → fix/restart `sshd` on 2222; confirm `status.sh`).
2. **Re-bind deploy SHA** (`cat .deployed_sha` == `9543f387…` or later intentional main) + re-run self-smoke SMTP + **IMAP reply stop**.
3. **Human-edit/approve** only rewritten drafts (not raw template sample).

---

## Outcome memory honesty

Live receptor is `serve-outcomes --memory-store` (in-process). HMAC, idempotency, and no-auto-WON were proven. **Durable Decision Memory DB was not the production receptor path.** Treat durable DM as follow-up ops work, not a silent PASS.

---

## Non-blockers (explicit)

- Draft quality / empty why_you (process with human rewrite; not a silent safety hole)
- Consumer `EMAIL_SENT` handler warn on generic send path
- CodeQL historical noise on web-cfg #56 (non-required)
- SEO indexation

---

## Re-GO checklist (operator)

### 0. Restore SSH first (Netcup SCP VNC)

Host is up (ping + `:8443`) but **sshd is not accepting on 2222/22**. Agent cannot recover this without console.

1. https://www.customercontrolpanel.de/ → RS 2000 → **SCP** → **VNC Console**
2. As root: `systemctl status ssh || systemctl status sshd`; `ss -lntp | grep -E '22|2222'`
3. Fix/restart sshd; confirm laptop `ssh ec-prod 'hostname'`

### 1–3. Re-bind evidence (must be fresh, not historical)

```bash
ssh ec-prod
cd /opt/warmbly-confenge
git fetch origin main && git checkout --detach origin/main
# restore deploy/confenge-vps/.env from /root backup if needed
echo "$(git rev-parse HEAD)" > .deployed_sha
test "$(cat .deployed_sha)" = "$(git rev-parse HEAD)"
bash deploy/confenge-vps/status.sh
docker exec warmbly-confenge-backend-1 cat /data/confenge-ops/kill-switch
CONFENGE_SELF_SMOKE_TO=<operator-owned@you> bash deploy/confenge-vps/self-smoke.sh
# Reply from a second operator client → Unibox IMAP ingest + touchpoint cancel
# Human-rewrite drafts (why_you/micro_offer empty in raw sample) before any lead send
# Re-emit GO only if SHA MATCH + SMTP + IMAP reply-stop + status PASS
```

Until then:

```text
NO_GO
```
