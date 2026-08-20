# Organic attribution contract `confenge.organic_attribution.v1`

Warmbly consumes organic source/asset fields on `confenge.commercial_event.v1`.
This is not a CRM object, not a GSC warehouse, and not a write into web-cfg.

## Authority

| Plane | Owns |
| --- | --- |
| web-cfg | source, referrer class, landing path, asset/CTA versions, consent, GSC/site aggregates |
| extra-cli | facts, entities, evidence |
| Warmbly | approved action, acknowledgement, reply, meeting, proposal, observed outcome |

Individual Search Console queries never join a person or lead. `query_hash`
is aggregate / k-anonymous only. Page view is not a lead. Lead is not
pipeline. Pipeline is not revenue. `unknown` is never rewritten to
`direct` or `organic_search`.

## Source taxonomy

Exactly one of:

`organic_search` | `direct` | `referral` | `ai_referral` | `partner` | `outbound` | `unknown`

Producer identities such as `web-cfg` or `CONFENGE_WEB` stay on `source`
and map to `organic_source=UNKNOWN` unless an explicit organic token is
provided. Slices never mix.

## Preserved when supplied

`medium`, `campaign`, `referrer_class` (never a sensitive URL), canonical
`landing_path`, `asset_family`, `asset_id`/`asset_version`, CTA id/version,
aggregate `intent_class`/`query_class`, `correlation_id`/`lead_id`/`account_id`,
`record_kind` real vs synthetic, consent/version, observed first_touch and
last_touch, clocks, page/content version, deploy SHA.

## Exceptions (no silent drop)

`lead_without_asset_id`, `unknown_asset_version`, `contradictory_source`,
`synthetic_treated_as_real`, `missing_consent`, `pipeline_without_evidence`,
`revenue_without_financial_event`, `gsc_query_on_lead`, `query_hash_on_lead`,
plus the existing join codes (duplicate, out_of_order, negative_latency).

## Exports

- `GET /confenge/intel/organic-scoreboard` (`confenge.organic_learning_scoreboard.v1`)
- `GET /confenge/intel/organic-feedback` (`confenge.organic_editorial_feedback.v1`)

Feedback verdicts are exactly `REPEAT` | `CHANGE` | `STOP` | `NEED_MORE_DATA`.
`causal_proof` is always false. `upstream_writes` is always empty.
