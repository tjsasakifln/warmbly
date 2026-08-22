# Dossier reference on a commercial card

`confenge-dossier/1.0` is produced by extra-cli
(`python3 -m scripts.dossier build --cnpj <CNPJ14> --out <DIR>`). It is the
machine core of the paid offer `CFG-DIAG-EXP-v1`. The consumer contract lives
in `extra-cli/docs/contracts/confenge-dossier-v1.md`, and the decision it
records for this repository is verbatim:

> May attach a dossier reference to an outreach touchpoint. Must never embed
> the private dossier body in an outbound message. The dossier is delivered by
> a human, not auto-attached.

This page describes the Warmbly side of that seam.

## What crosses the boundary

The producer writes four files. Warmbly reads exactly one of them.

| File | Contains the prospect? | Warmbly |
| --- | --- | --- |
| `dossier.json` | yes | never read, never stored |
| `dossier.md` | yes | never read, never stored |
| `public-read.json` | no | belongs to web-cfg, not to Warmbly |
| `manifest.json` | no | the only input to this seam |

From `manifest.json` only these fields are persisted: `dossier_id`, `schema`,
`catalog_mode`, `data_state`, `as_of`, `content_hash`, `public_content_hash`,
`producer_sha`, plus a local filesystem path or URI the operator supplies. No
dossier body, and no prospect identity beyond what the `outreach_accounts` row
already holds.

`ParseDossierManifest` refuses any payload carrying a body section
(`identity`, `buyer_map`, `competitors`, `price_panel`, `expiring_contracts`,
`open_opportunities`, `findings`) or an identity field (`cnpj14`, `cnpj_raiz`,
`razao_social`, `nome_fantasia`, `supplier_cnpj`, `supplier_nome`,
`fornecedor_cnpj`, `fornecedor_nome`, `municipio`). The scan bounds recursion at
32 levels and treats reaching that bound as a refusal, because declaring a
deeply nested payload clean is the opposite of the guarantee.

That scan inspects key **names**, which on its own is not enough: an adversarial
review put 126 KB of `dossier.md` into `dossier_id` and `razao_social=...;cnpj14=...`
into `as_of`, and both were stored. Every persisted scalar is therefore also
format- and length-pinned, in the application and again as a SQL `CHECK`
(migration `000115`):

| Field | Bound |
| --- | --- |
| `dossier_id` | `^[A-Za-z0-9_.:-]{1,128}$` |
| `content_hash`, `public_content_hash` | `^sha256:[0-9a-f]{64}$` |
| `as_of` | `^\d{4}-\d{2}-\d{2}$` |
| `producer_sha` | `^[0-9a-f]{7,64}$` |
| `artifact_uri` | `^[A-Za-z0-9_./:-]{1,512}$` |
| `delivery_note` | 2000 bytes, and scanned for a CNPJ or an identity field |

`delivery_note` is the one field a human types, so it cannot be format-pinned.
It is bounded and scanned by value instead: a CNPJ or a `razao_social:` in the
note is the same leak as one in a column.

Handing `dossier.json` to the attach call is rejected with `422`, and nothing
is written.

## This is not a send path

A dossier reference is metadata on a card. Attaching one does not queue,
approve, dispatch, or send anything. It does not touch auto-send, GREEN
autorun, the dispatch governor, the daily cohort cap, or the kill switch.
`CONFENGE_AUTO_SEND_ENABLED` stays `false`, and no card flag
(`actionable`, `email_sendable`, `dispatchable`) changes when a badge is
stamped. `TestAttachingDossierReferenceReachesNoSendPath` pins that: the three
dossier source files may not even name a send-path symbol.

## Deliverable is derived, never asserted

| `catalog_mode` | `data_state` | Deliverable |
| --- | --- | --- |
| `official_live` | `DATA_READY` | yes |
| `official_live` | `DATA_HOLD` / `DATA_REJECT` | no |
| `fixture` | any, including `DATA_READY` | no |

A non-deliverable manifest is still storable. That is deliberate: the operator
should see that a dossier run exists and why it is not ready. It is marked
`deliverable=false` with a reason, the card badge reads
`Dossie NAO entregavel (...)`, and the reason is appended to the card
warnings. It is never presented as ready to hand to a client.

The rule is enforced in three places: `DossierDeliverability` in Go, the
`confenge_dossier_references_deliverable_check` constraint in SQL, and the
`WHERE ... AND deliverable` clause on the delivery update.

## Delivery is a human act

`delivered_at` and `delivered_by` default to null and are only written by
`MarkDossierReferenceDelivered`, which the operator calls after handing the
dossier over. Delivery is never inferred from attachment, from an outcome, or
from a send.

Three guards keep that honest:

- `NormalizeDossierReference` refuses a reference that arrives pre-stamped
- `MarkDossierDelivered` refuses a nil actor and refuses a non-deliverable reference
- SQL keeps `delivered_at` and `delivered_by` paired, and gated on `deliverable`

## Storage

Migration `000114_confenge_dossier_reference` creates
`confenge_dossier_references`, keyed by `(organization_id, account_id,
dossier_id, content_hash)` so re-attaching the same artifact is idempotent and
a re-run with new data lands as a new row. Optional
`commercial_action_id` and `touchpoint_id` bind the reference to the specific
card or touchpoint the operator was working.

## API

Go seam in `internal/app/confenge/`:

```go
WireDossierReferences(store DossierReferenceStore)
AttachDossierReference(ctx, orgID, actorID uuid.UUID, in DossierAttachInput) (*DossierReference, *errx.Error)
ListDossierReferences(ctx, orgID, accountID uuid.UUID) ([]DossierReference, *errx.Error)
MarkDossierReferenceDelivered(ctx, orgID, actorID, refID uuid.UUID, note string) (*DossierReference, *errx.Error)
```

`DossierAttachInput` takes the raw `manifest.json` bytes, so the private-body
guard cannot be bypassed by constructing the struct by hand.

Both mutations audit through `AuditService.LogAction` with the
`outreach_account` entity type, so the existing realtime spine invalidates the
confenge queries for every teammate with no new frontend mapping.

## Files

- `internal/infrastructure/db/migrations/000114_confenge_dossier_reference.{up,down}.sql`
- `internal/app/confenge/dossier_reference.go` (types, guards, memory store)
- `internal/app/confenge/dossier_reference_service.go` (service seam)
- `internal/app/confenge/dossier_reference_store_pg.go` (durable store)
- `internal/app/confenge/commercial_card.go` (`DossierBadge`, `ApplyDossierBadges`)
- `internal/app/confenge/dossier_reference_test.go`
