# Migration QA — 000106

| Check | Result |
| --- | --- |
| Main at merge | `ab37782632922f6f150756c9686f0869df4add19` |
| CI product acceptance migrate | pass after quoting reserved `"window"` |
| Production `schema_migrations` | `106` (not dirty) |
| Table | `outreach_intel_search_observations` |
| Unique | `(organization_id, event_id)` |
| Canary row | `canary-so-convergence-03` synthetic, window `28d_complete`, eligible=7 appeared=3 clicked present engaged null |
| Down | drops the table; restores 000105 exception CHECK |

Local `WARMBLY_TEST_POSTGRES_DSN` was unset; MemoryStore covered unique/replay/list. Production apply is the live proof.
