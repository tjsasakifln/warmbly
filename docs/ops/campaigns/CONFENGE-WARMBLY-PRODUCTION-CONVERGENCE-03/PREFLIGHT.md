# PREFLIGHT — CONFENGE-WARMBLY-PRODUCTION-CONVERGENCE-03

Observed 2026-08-20.

| Item | Value |
| --- | --- |
| origin/main | `ec8647e211605329a414094e0fd1df1846947f93` |
| dirty checkout (not used) | `feat/confenge-inbound-last-mile` @ `9cadc94a` |
| source PR | [tjsasakifln/warmbly#98](https://github.com/tjsasakifln/warmbly/pull/98) @ `b81e8c888a5f390209daa6be5525a7f71ccb194b` |
| worktree | `/home/tjsasakifln/code/confenge/warmbly-production-convergence-03` |
| branch | `integration/confenge-warmbly-production-convergence-03` |
| latest main migration | `000105_outreach_operator_alerts` (000106 still free) |
| PR #98 CI | Security Scan green, CONFENGE product acceptance green, Go CI red |
| Go CI root cause | `TestInboundCommercialSkipKeepsRealInQueue` expected query=`segunda leitura contrato`; sanitizer correctly emits `UNKNOWN` |
| main branch protection | none at cut |

This campaign does not edit `feat/confenge-inbound-last-mile`.
