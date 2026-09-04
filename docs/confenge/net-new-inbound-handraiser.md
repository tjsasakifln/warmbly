# NET_NEW_INBOUND_HANDRAISER consumer (campaign 07)

Warmbly consumes `NET_NEW_INBOUND_HANDRAISER/1.0.0-draft.20260904` on the
existing inbound HMAC route. This is not a second CRM. Receipts land on
`outreach_inbound_leads`, net-new people/companies reuse
`AdmitInboundOnly`, and accepted events converge onto one commercial
action through `ConvergeHandRaise`.

Draft contract IDs are a fail-closed pin. A missing or divergent
`schema_hash` / `policy_hash` is never `ACCEPTED`. Goal 97 ratifies the
pin. HTTP 2xx is not acceptance; `outcome` plus readback of the same
`logical_id` is.

## Pin

- Policy / envelope: `NET_NEW_INBOUND_HANDRAISER/1.0.0-draft.20260904`
- Intake: `CONFENGE_WEB_INTAKE/2.0.0-draft.20260904`
- State: `CONFENGE_HANDRAISER_STATE/1.0.0-draft.20260904`
- Meetcfg projection: `MEETCFG_HANDRAISER_CONTEXT/1.0.0-draft.20260904`
- Taxonomy: `CONFENGE_CORPORATE_TAXONOMY/1.0.0-draft.20260904`
- Catalog: `CONFENGE_OFFER_CATALOG/2.0.0-draft.20260904`
- Hash: `92bafd8b644b1355bcf457e2aa55a7a902030234cc65139bd1c2a24ff880a30b`
- Source / lane: `CONFENGE_WEB`
- Invariants: `outbound_eligible=false`, `auto_send=false`

Fixture for campaigns 08 and 14:
`internal/app/confenge/testdata/net_new_inbound_handraiser/conformance.json`

## Outcomes

Closed set: `ACCEPTED | REJECTED_WITH_REASON | UNKNOWN`.

- Persist the receipt before admit or queue writes.
- `ACCEPTED` creates or updates one inbound-only hand-raiser. Meetcfg
  handoff is allowed only then.
- Canonical entity ID reuses the existing account. The same display name
  without that ID does not merge.
- Conflict storage is a protected `conflict_ref` only.
- INTEL_WATCH / Live Intelligence factual envelopes stay on their own
  schema and never create a CONFENGE_WEB hand-raiser here.

## Readback

`GET /confenge/inbound/handraisers/:logicalId` returns `acknowledged_by`,
`acknowledged_at`, policy version, receipt, reason, and outcome for the
same logical ID.
