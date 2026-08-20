# EVIDENCE — CONFENGE-WARMBLY-OFFER-TO-REVENUE-RECONCILIATION-01

## Residual before this change

This tree already had `confenge.inbound.v1` + `confenge.commercial_intel.v1`
+ `confenge.commercial_event.v1` on origin/main (PR #80, offer-revenue-04).
HMAC `POST /api/v1/webhooks/confenge/inbound` advertised
`confenge.commercial_event.v1` but only ingested leads and search
observations. There was no explicit `ONBOARDING_*` decision and no
`by_offer_version` executive row.

Producer contract consumed: tjsasakifln/web-cfg `scripts/offers/events.cjs`
(`schema`, `event_id`, `type`, `occurred_at`, `offer_id`/`offer_version`/
`terms_version`, `external_reference`, `provider_event_id`,
`provider_raw_status`, `canonical_status`, `amount_cents`, `currency`,
`source`, `financial_confirmation`, `received_revenue`, `revenue=false`,
`exception_code`). No Warmbly schema fork.

## Shipped path

- `ParseProducerCommercialEvent` / `NormalizeProducerCommercialEvent`
- HMAC inbound → `IngestCommercialEvent` → `IngestEvent`
- `DecideOnboarding` (`ONBOARDING_BLOCKED|ELIGIBLE|STARTED|SERVICE_ACTIVE`)
- `Rollup.ByOfferVersion` + stage timestamps for #55 (no SLA)

## Fixture evidence (SYNTHETIC IDs)

```
E2E durable_receipt=true duplicate=true payment_received=800000 onboarding=ONBOARDING_ELIGIBLE qualified_pipeline=0 received_revenue=9800000 by_offer=2
VERDICT=COMMERCIAL_RECONCILIATION_READY residual=live_consented_event_required
HTTP_COMMERCIAL invalid_secret=401 created=201 replay=200 unknown=201
UNKNOWN_EVENT held=true raw=TELEPORTED canonical=UNKNOWN
```

## Classification

| Label | Status |
| --- | --- |
| CODE_PROVEN | yes (synthetic fixtures) |
| LIVE_PROVEN | no |
| #47 closed | no (real consented event still required) |
