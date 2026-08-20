# FINAL — CONFENGE-WARMBLY-PRODUCTION-CONVERGENCE-03

Token: **PRODUCTION_CONVERGED**

## What is live

- `origin/main` = `ab37782632922f6f150756c9686f0869df4add19` (PR #99 squash merge)
- Netcup `/opt/warmbly-confenge` `.deployed_sha` matches that SHA
- Migration `000106` applied (`schema_migrations=106`)
- Inbound health READY with `accepted_event_versions` including `confenge.search_observation.v1`
- Search observations persist on `outreach_intel_search_observations` (MemoryStore + PGStore)
- Organic scoreboard/feedback API and `confenge intel-organic` operational
- Individual query stays UNKNOWN; canary with raw query returned 400
- Synthetic lead persisted, not in default INBOUND NOW, no new operator Mailpit message
- `CONFENGE_AUTO_SEND_ENABLED=false`, GREEN false, WhatsApp false, human approval true, kill switch paused
- PR #98 closed as superseded (not merged separately)
- Issue #47 reopened and left open: GitHub auto-closed it from a cherry-pick subject containing `#47`; DoD is not met

## What is not proven

- No real consented organic lead with observed human action and outcome
- Consumer and discovery capability are LIVE; real commercial evidence remains UNKNOWN / `NEEDS_WEB_CFG_EVENT` at `include_synthetic=0`
- Do not close #47 on this campaign

## Honest recommendation

`NEEDS_WEB_CFG_EVENT` on the real layer (synthetic search observation excluded by default). `include_synthetic=1` shows the canary discovery layer without promoting it to real or setting `causal_proof`.
