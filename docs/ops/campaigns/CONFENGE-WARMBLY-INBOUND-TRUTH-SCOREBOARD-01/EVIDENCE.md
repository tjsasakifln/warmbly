# Evidence — CONFENGE-WARMBLY-INBOUND-TRUTH-SCOREBOARD-01

## Verdict

Production path: **BLOCKED**.

Single human blocker: `CONFENGE_INBOUND_WEBHOOK_SECRET` is unset in this environment, so a live signed canary cannot be posted. Public `GET https://api.confenge.com.br/api/v1/webhooks/confenge/inbound/health` is already `READY` with `auto_send_enabled=false` and `dispatch_attempted=false`.

Next real event to record later: the first consented non-synthetic form POST (`lead_id` on INBOUND NOW), then `attempted` / `reached` via `POST /confenge/intel/human-outcomes` or `scripts/confenge_human_outcome.sh`. Do not enable auto-send. Do not fabricate WON, LOST, or receita.

## What shipped (reuse, not rewrite)

- Persist-first inbound: `POST /api/v1/webhooks/confenge/inbound` + `IngestInboundLead`
- HMAC: `SignOutcomeHMAC` / `VerifyOutcomeHMAC`
- `confenge.commercial_event.v1`: `IngestEvent`
- Exception queue with `owner`, `reason`, `next_action`
- INBOUND NOW skip of synthetic / qa / internal / `infrastructure_canary`
- Additive seven-stage scoreboard: `GET /confenge/intel/scoreboard`
- Founder capture: `POST /confenge/intel/human-outcomes`, `GET /confenge/intel/human-envelopes`, dashboard form, CLI

## Tests (gating)

```
go test ./internal/app/confenge/intel/ ./internal/app/confenge/ ./internal/api/handler/ -count=2
```

Observed:

- first inbound persist then byte-equivalent replay is a duplicate
- invalid HMAC is 401
- unknown / out-of-order / retry / nil store produce a queue row with owner, reason, next action
- auto-send and dispatch stay false
- synthetic / infrastructure_canary absent from INBOUND NOW and `include_synthetic=0` scoreboard
- seven stages keep impression, lead, proposal, and cash distinct (1-2 BLOCKED; receita not TRUE without a document)
- WON / LOST / receita without evidence stay held / UNKNOWN
- EXTRA + ACCOUNT_1 + ACCOUNT_2 + ACCOUNT_3 invent no IDs and replay idempotently

`gofmt -l` on touched Go: empty. golangci-lint on touched packages: pass. `pnpm typecheck` in `web/`: pass.

## Env audit (names only)

See captured `env-audit.log`. Secret / dest org / caller URL unset here. Public health READY. Loopback API not running. `api.warmbly.com` NXDOMAIN.

## Deploy

No production deploy from this campaign. Receive path is already READY. Rollback of this PR is `git revert` of the merge commit. Auto-send stays off.

## Rollback

```
git revert <merge-sha>
```

Does not rotate the inbound secret. Does not enable send or billing.

## Commit

`427b9ca321d05972e68b20171b7dccaf8d2bf99c` on `goal/inbound-truth-scoreboard-20260818` (cut from origin/main `58eb3308`).

## Artifact hashes

```
ed2204bca6a1e6bec34b5ddb6ee9bc116b76e7b84de6d787cf42c63a4a648249  CANARY_MANIFEST.json
4755a9154dfb251c07ca338646aa779672b2a1890b92c356bac371bd2ab4e5b5  EVIDENCE.md
9014027839a56771c8c2e78fbc9b09689d061d45319d6063861534de84e70241  EXCEPTION_CASES.json
a0796556dd7a02b70f3faa747c0a613069ba4fe692d53f85bee9463cc2ccde63  FOUNDER_ACTION_REQUIRED_INBOUND.txt
9b51dead5e13673a54c271acf42077070a2cbe17ad2c450cd52b5c90139d021b  HUMAN_OUTCOME_ENTRY.txt
d848c2272e5c584d5db620d6b84b60d04910f54b722d9709735ed1b214675f69  IDEMPOTENCY_REPLAY.md
84e224802be4e05cf08d121b009acd53a8f9a4e1366904bf6ce42532b322008f  SCOREBOARD_CURRENT.json
1ba8a9386e97623b16f85eb7487c24d93804ee7b95856fd35aaba133b63a0be1  SCOREBOARD_SCHEMA.json
8e2a1b0a2444a1267ae67ae3f9baa2f1abff4cdd20d684b031c3f3b0ed0e52ce  envelopes.json
```
