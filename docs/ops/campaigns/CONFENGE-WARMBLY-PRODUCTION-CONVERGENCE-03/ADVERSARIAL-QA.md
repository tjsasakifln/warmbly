# Adversarial QA — CONFENGE-WARMBLY-PRODUCTION-CONVERGENCE-03

Local bar on worktree `integration/confenge-warmbly-production-convergence-03`.

## Gating

| Check | Result |
| --- | --- |
| `gofmt -l` on touched Go | empty |
| `golangci-lint` on confenge/handler/cmd/confenge | pass |
| `TestInboundCommercialSkipKeepsRealInQueue` `-race` | pass; query=`UNKNOWN`; no raw phrase/GSCQuery/query_hash on chain, INBOUND NOW, alert, scoreboard, feedback, report |
| intel package `-count=2` | pass twice |
| intel+handler `-count=2 -race` | pass |
| inbound/search/alert/scoreboard tests `-count=2` | pass |
| CLI `confenge intel-organic` twice | identical: schema `confenge.organic_learning_scoreboard.v1`, `causal_proof=false`, `include_synthetic=false`, `real_empty=true`, `recommendation=NEEDS_WEB_CFG_EVENT`, no query/hash |
| CLI `--include-synthetic` | `causal_proof` stays false; canaries stay labeled |
| synthetic search observation + transporter | 0 operator mail |
| auto_send=true | ingest refused |
| unsigned/invalid HMAC | 401 |
| unsupported search observation version | 4xx, no persist |
| raw query on search observation | 4xx |
| MemoryStore unique `(org,event_id)` replay | pass |
| PGStore search observations | skipped unless `WARMBLY_TEST_POSTGRES_DSN` |

## Schema/ingest matrix covered in `intel/search_observation_test.go` and inbound HTTP tests

version missing/unknown, field missing, negative count, nullable count, invalid window, invalid source, query literal, query_hash, future measurement_at, duplicate replay, cross-org isolation, synthetic labeled real held, unknown type does not persist a chain.

## Scoreboard

discovery without lead → `NEEDS_REAL_EVENT` (or `NEEDS_WEB_CFG_EVENT` when the only snapshot is synthetic and `include_synthetic=0`); lead without discovery → `NEEDS_WEB_CFG_EVENT`; both real → `READY_FOR_INTEGRATION`; ABSENT/UNKNOWN/null not coerced to observed zero; sources unmixed; contracted vs received distinct; n<5 latency UNKNOWN; never `LIVE_PROVEN`.

## Residuals (local)

- `make check-proto`: `protoc` not installed here; CI owns this check
- `TestProductAcceptanceMultichannelSum`: Mailpit ports closed here; CI job `CONFENGE product acceptance` is the production bar
- live Postgres DSN unset: MemoryStore is the local bar; PG test is skip-gated
- Trivy/SBOM/Playwright live stack not run here
