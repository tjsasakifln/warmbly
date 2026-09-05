# NET_NEW_INBOUND_HANDRAISER consumer (REV-03)

Warmbly consumes a multi-vertical `NET_NEW_INBOUND_HANDRAISER` hand-raiser
on the existing inbound HMAC route. This is not a second CRM. Receipts
land on `outreach_inbound_leads`, net-new people/companies reuse
`AdmitInboundOnly`, and accepted events converge onto one commercial
action through `ConvergeHandRaise` / `PersistHandRaise`.

Admission is fail-closed on REV-02 `contract_id + version + content hash`.
Until a final REV-02 SHA is recorded in `RuntimeRev02Pin`, the matching
adapter is test-only. Fixture schemas are never production authority.
HTTP 2xx is not acceptance; `outcome` plus readback of the same
`logical_id` is.

## Pin

- Contract ID: `NET_NEW_INBOUND_HANDRAISER`
- Version: `1.0.0-draft.20260904`
- Canonical: `NET_NEW_INBOUND_HANDRAISER/1.0.0-draft.20260904`
- Content hash (test-only adapter): `92bafd8b644b1355bcf457e2aa55a7a902030234cc65139bd1c2a24ff880a30b`
- Observed REV-02 HEAD (not a final pin): `230d73a22a321112abe09b34a0d5fe743790b857` (`tjsasakifln/Governance`)
- Runtime pin: unpinned (`WAITING_GOVERNANCE_PIN`)
- Source / lane: `CONFENGE_WEB`
- Invariants: `outbound_eligible=false`, `auto_send=false`

Test-only fixture: `internal/app/confenge/testdata/net_new_inbound_handraiser/conformance.json`

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
`acknowledged_at`, policy version, hash, receipt, reason, and outcome for
the same logical ID.

## Metrics

Nucleus, state, and reason only. No protected payload, email, name, or
conflict corpus.
