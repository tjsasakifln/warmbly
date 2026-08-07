# CONFENGE product acceptance

Version: 1.0  
Date: 2026-08-07  
Branch: `feat/confenge-product-acceptance`  
Scope: prove that `extra-cli` + Warmbly form a local commercial engine for CONFENGE multichannel outreach (email + eligible WhatsApp) with factual personalization and human approval of every message.

## Preconditions

All required foundation surfaces are **PRESENT** on the integration tip (orchestration #6 + grounded copy #7 + governor #8 + per-touch #9 + reply cockpit #10 + local-first #11). See the inventory captured in the PR body and goal scratch `preconditions.md`.

If any surface were missing, this matrix would mark product readiness **BLOCKED** rather than PASS.

## Acceptance matrix

| Area | Result | Evidence |
| --- | --- | --- |
| Intelligence | PASS | Feed import of multi-company `confenge.outreach.v1`; service codes and fact-to-mention from dossier; grounded generate + lint (`generate.go`, `lint.go`, `TestProductAcceptanceMultichannelSum` bullets 1–3) |
| Contact | PASS | Exact recipient on draft/touchpoint; NEEDS_CONTACT when no email; no invented opt-in (`product_acceptance_e2e_test.go` bullet 4; `whatsapp_orchestrator_test.go`) |
| Email | PASS | Human-approved content hash bound to touchpoint; mock transport capture of approved body (Mailpit optional via `CONFENGE_MAILPIT_URL`; CI uses capture) |
| WhatsApp | PASS | Orchestrator cases A–E; public phone without opt-in blocked; consented eligible/template only; mock Evolution provider in unit tests (no real WABA) |
| Approval | PASS | No transport before approval; approval by exact content hash; edit invalidates; reply draft stays NEEDS_REVIEW (`touchpoint_sm.go`, product E2E bullets 5–8, 18) |
| Pacing | PASS | Global governor shared email+WhatsApp cap 10/60min; 11th blocked; restart no burst (`dispatch/governor_test.go`, product E2E bullets 9–11) |
| Replies | PASS | Inbound handoff pauses cadence; Needs attention list; reply draft never auto-sends (`reply_handoff.go`, `reply_cockpit.go`, product E2E 15–18) |
| Outcomes | PASS | HMAC sign/verify; test receiver idempotent; reimport preserves DNC (`outcomes.go`, product E2E 19–20) |
| Local startup | PASS | `cmd/confenge`, preflight, readiness, kill switch, `docs/confenge/local-ops.md` (PR #11 on tip) |
| Security | PASS | Feature flags fail-closed; AI cannot approve or send; multi-tenant org scoping on service methods; no secrets in fixtures |
| No-real-send tests | PASS | Product E2E and package tests use in-memory repo, mock WA provider, httptest outcome receiver, fake clock; no live lead sends |

## Scenario bullets (20)

All exercised by `TestProductAcceptanceMultichannelSum` (and sibling unit tests where noted):

1. Multi-company import — PASS  
2. Distinct dossiers/services — PASS  
3. Different messages per account — PASS  
4. Exact recipient visible — PASS  
5. No message before approval — PASS  
6. Approval by exact content hash — PASS  
7. Edit invalidates approval — PASS  
8. Approved and queued — PASS  
9. Governor max 10 outbound / 60 min across channels — PASS  
10. 11th remains queued/blocked — PASS  
11. Restart does not burst — PASS  
12. Email content matches approved (mock Mailpit capture; live Mailpit operator-only) — PASS (CI mock)  
13. WhatsApp only eligible/consented — PASS  
14. Public phone without opt-in blocked — PASS  
15. Inbound reply pauses cadence — PASS  
16. DNC cancels next touches — PASS  
17. Reply → Needs attention — PASS  
18. Reply draft not sent without new approval — PASS  
19. Outcomes via HMAC, idempotent — PASS  
20. Reimport preserves sent/replied/DNC — PASS  

## Real provider non-claims (operator smoke, not code failure)

CI without secrets does **not** prove:

- Real inbox deliverability (Gmail/Microsoft reputation)
- Real Microsoft 365 tenant Graph send
- Real WhatsApp Business Account (WABA) connectivity
- Meta-approved marketing template
- Opt-in provenance for real leads

Treat those as **operator smoke** on owned addresses only. Never send to real leads in CI or acceptance automation.

## Operator smoke (Tiago)

Use only self-owned destinations. Do not message real leads.

### One email to your own address

```bash
# With local stack (make infra + make backend + Mailpit)
export CONFENGE_ENABLED=true
export CONFENGE_REQUIRE_HUMAN_APPROVAL=true
# Import demo fixture, plan/generate, human-approve in dashboard /app/confenge,
# then queue with dispatch governor. Verify the approved body in Mailpit UI
# (default http://localhost:8025) matches the review pane exactly.
```

### One WhatsApp to your own consented number

```bash
# Only after Evolution is configured for a sandbox instance and YOUR number
# has USER_INITIATED or OPTED_IN with provenance_ok. Never free-text cold to
# public phone book numbers.
export CONFENGE_WHATSAPP_ENABLED=true
# Approve a WHATSAPP touchpoint in the UI, then send via the gated path.
```

### Local outcome receptor

```bash
# Terminal A: HMAC-aware receiver (example)
# python3 -m http.server is NOT enough; use a small HMAC checker or:
go test ./internal/app/confenge/ -run TestProductAcceptanceMultichannelSum -count=1 -v

# Production-shaped env for worker delivery to extra-cli:
export CONFENGE_OUTCOME_WEBHOOK_URL=https://127.0.0.1:9999/outcomes   # HTTPS in prod
export CONFENGE_OUTCOME_WEBHOOK_SECRET=whsec_local_only_not_committed
```

### Local-first one command (when ops script is wired)

```bash
# See docs/confenge/local-ops.md
go run ./cmd/confenge preflight
go run ./cmd/confenge readiness
```

## Playwright UI

Scoped exception for this acceptance front only:

- Spec: `web/e2e/confenge-product-acceptance.spec.ts`
- Path: open CONFENGE → evidence → edit → approve → quota → sent → inject reply fixture → Needs attention

If browsers cannot install in the environment, service E2E remains the behavioral gate; UI presence is covered by static routes/components under `web/src/app/app/confenge/`.

## How to re-run

```bash
go test ./internal/app/confenge/ -run TestProductAcceptanceMultichannelSum -count=1 -v
go test ./internal/app/confenge/dispatch/ -run 'TestCap10|TestRestart|TestEmailAndWhatsApp' -count=1 -v
go test ./internal/app/confenge/ -count=1
```

## Dependencies

This PR is integration/acceptance only. It depends on open foundation and feature PRs:

- #4 full stack import/review/enroll/outcomes/CRM  
- #5 WhatsApp transport  
- #6 WhatsApp orchestration (base tip)  
- #7 grounded copy  
- #8 dispatch governor  
- #9 per-touch approval  
- #10 reply cockpit  
- #11 local-first ops  

Do not merge this PR before those land (or land as a coordinated stack). After deps merge, rebase onto updated `main` so the final diff is only acceptance/proof assets where possible.
