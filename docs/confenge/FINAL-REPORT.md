# CONFENGE FINAL PRODUCTION READINESS — warmbly FINAL-REPORT

Generated: 2026-08-08T01:40:15.434105+00:00
Branch: `feat/confenge-final-integration-01`
HEAD (pre-commit snapshot): `7c27e100d5f070a198659d7f697df78268f07b50`

## Divergence from campaign brief

- Campaign cited warmbly PR **#13** as canonical. Real PR #13 is an older **merged** "dev stack" PR (`fix/dev-stack-and-worktree-split`), not CONFENGE.
- Canonical work lives on branch `feat/confenge-final-integration-01` (no open confenge PR existed at start). A new PR to `main` will be opened/updated after this commit.
- extra-cli PR **#206** matches the brief (`campaign/confenge-final-integration-01`).

## Root causes fixed

### Phase A — Playwright live
- **CORS**: backend `CORS_ALLOW_ORIGINS` only listed `http://localhost:5173` while Playwright uses `http://127.0.0.1:5173`, so SPA API calls failed and `/app/confenge` redirected away when status.enabled never loaded.
- Fixed env to include both origins. Live Playwright **PASS** (import → approve hash → edit invalidates → re-approve → queue/SENT) against real stack.

### Phase B — False-green CI
- Removed `continue-on-error: true` from `CONFENGE product acceptance`.
- `CI Status` now fails on confenge `failure` / `cancelled` / `timed_out` and unexpected `skipped` when web/go changed.
- Structural test `TestConfengeProductAcceptanceIsHardCIGate` prevents regression.

### Phases C–F — Real universe + feed
- Full national universe already proven: 3,689,859 contracts → 48,748 eligibles, `full_scale=true`, reconciliation OK.
- Fingerprint bug fixed: `MAX(contrato_id)` → physical `id` column mapping.
- Contact resolution empty-cache poison fixed: cache key includes `allow_network`.
- Re-ran contacts with `--allow-network`: **95/200** public registry phones; **0** verified emails (honest); WhatsApp opt-in **0**.
- Real `confenge.outreach.v1` feed regenerated; acceptance slice with provenance + operator sinks.

### Phases G–H — Playwright + Mailpit
- Playwright defaults to `data/confenge-feeds/acceptance_real_slice/slice.json` (real pipeline slice).
- Live proof: approved content path + queue/SENT on local sink stack.

## Gate board (latest gate run)

See `data/confenge-evidence/GO-NO-GO.md` — verdict `READY_FOR_CONTROLLED_REAL_OUTREACH` on measured gates with pilot-list channel.

| Channel | Status |
|---------|--------|
| PRODUCT_CORE_READY | PASS (product gates measured) |
| EMAIL_REAL_CHANNEL_READY | PASS controlled (operator pilot sinks + Mailpit; **not** verified prospect emails) |
| WHATSAPP_REAL_CHANNEL_READY | BLOCKED_EXTERNAL / policy-mock only (no real WABA; public phone ≠ opt-in) |
| OVERALL_PILOT_MODE | EMAIL_ONLY controlled |

## Absolute: no real lead sends in this campaign

Transport sinks only (`@warmbly.local` / Mailpit).

## Migrations preserved

000080–000087 confenge series unchanged; no reintroduction of `000080_whatsapp_channel` conflict.
