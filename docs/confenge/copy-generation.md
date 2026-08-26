# CONFENGE copy generation (evidence-grounded)

Warmbly redacts and validates commercial copy from the **imported extra-cli dossier** only.
There is **no research** in this path: the model receives structured JSON and returns structured JSON.

## Prompt version

| Version | Notes |
| --- | --- |
| `confenge.draft.v1` | Initial evidence-bound email generator + template fallback |
| `confenge.draft.v2` | Channel-aware modes, `claims[]`, internal `rationale`, anti-template linter, near-dup single regen |
| `confenge.draft.v3` | Strategy-first composition (`OutreachStrategy`), doctrine `confenge-outreach-v1`, micro-offers, doctrine QA |
| `confenge.draft.v4` | Messageability gate + outbound-safe plan (`confenge.composer.v2`, doctrine `confenge-outreach-v2`). Internal strategy fields are never interpolated. Unsent prior-version drafts must be regenerated. |
| `confenge.draft.v5` | Authorizable NEEDS_REVIEW. Renderer reads only `RecipientFacingCopy`. `validation_ok` still requires a separate authorization decision. P0 hard QA: leak phrases, crédito frame, near-dup, stale composer, dumps, empty copy, missing CTA `?`. |
| `confenge.draft.v6` | Semantic brief first, written event subjects, safe company-context fallback when a specific fact is absent, deterministic editorial QA, and bounded AI rewrite recovery. Suboptimal copy remains recoverable and returns to NEEDS_REVIEW before any fresh human or delegated-policy decision. |

Constant: `internal/app/confenge/validators.go` → `PromptVersion`. Current: `confenge.draft.v6`.
Doctrine: `OutreachDoctrineVersion` + `internal/app/confenge/outreach_playbook/`. Current: `confenge-outreach-v2`.

Bump the constant when the system prompt schema or hard safety rules change. Store the version on each `OutreachDraft.PromptVersion` for audit.

## Composer version

| Version | Notes |
| --- | --- |
| `confenge.composer.v2` | Messageability gate and outbound-safe plan. Internal strategy fields are never interpolated. |
| `confenge.composer.v3` | Fail-closed last-mile authorizable outreach. |
| `confenge.composer.v4` | De-shouts edital prose, collapses verbatim repeated n-grams, stops label stutter, terminates a fact without producing `,.`. |
| `confenge.composer.v5` | Editorial composer: semantic brief, deterministic fact selection, editorial QA, corpus QA across the cohort, and bounded recompose recovery. **Current.** |

Constant: `internal/app/confenge/messageability.go` → `ComposerVersion`. Current: `confenge.composer.v5`.

The composer stamp is what identifies frozen copy. A cohort snapshot and each of its members stamp `ComposerVersion`; a draft or touchpoint is identified by its `PromptVersion`, which is the composer that wrote the text.

## Editorial authority (frozen copy is operational only while its composer ships)

Warmbly ships one composer at a time. `internal/app/confenge/editorial_authority.go` is the single place that decides whether frozen copy is still operational, and every approve, authorize, queue and transport path asks it instead of comparing version strings itself.

| State | Meaning |
| --- | --- |
| `CURRENT` | The current composer (and, for cohorts, the current bounded-cohort policy) wrote this text. It can still be reviewed, approved, authorized, queued and sent. |
| `LEGACY_SUPERSEDED` | An earlier composer or policy wrote it. It stays readable for audit and is refused by every operational path. |

Reason codes: `composer_superseded`, `composer_unstamped` (fail-closed: no stamp is no proof), `policy_superseded`. Superseded artifacts also carry `EditorialLegacyNotice`, the PT-BR line a reader must see above the text.

Rules:

- `EvaluateCohortEditorialAuthority` checks composer plus `BoundedCohortPolicyV1`. `EvaluateDraftEditorialAuthority` checks `PromptVersion` only: the cadence policy stamped on a touchpoint is a schedule, not a composer.
- `SnapshotEditorialAuthority` is current only when the snapshot stamp and every member stamp are current, so a member left behind by a partial recompose cannot ride under a current header.
- `APPROVE` on a cohort candidate is refused with `composer_superseded`; `HOLD` and `REJECT` stay open so an old version can be closed out. `GO` is blocked by the same codes. Touchpoint approve, queue and transport are refused the same way.
- The transport check is absolute, not relative: a grant and its copy can agree with each other and still both be the work of a composer this build no longer ships.
- The way forward is `POST /confenge/cohorts/:id/recompose`, which forks version N+1 with the current composer and inherits no approval, no `GO` and no queue state.

Customer-facing docs: `docs/content/docs/guides/confenge-editorial-lifecycle.mdx`.

## Generation channels

| Kind | Persist channel | When |
| --- | --- | --- |
| `EMAIL_INITIAL` | EMAIL | First outbound email |
| `EMAIL_FOLLOWUP` | EMAIL | Same thread after ignored mail |
| `WHATSAPP_INITIAL` / `WHATSAPP_CONTINUATION` | WHATSAPP | Only if policy/consent allows generation |
| `REPLY_DRAFT` | EMAIL | Lead already replied; reuses confenge generate path (not unibox auto-send) |

WhatsApp generation **short-circuits** when consent/policy blocks the channel (`CONFENGE_WHATSAPP_ENABLED`, opt-in provenance, DNC). Free-text still never auto-sends without human approval.

## Manual route composers (CALL / ROUTED_CALL / WhatsApp / caixa genérica)

Founder-facing text on **Agir agora** and the fila manual is composed per route by `ComposeActionContent` (and `manualSuggestedText` / `BuildWhatsAppCopy` reuse that). None of those four routes reuse the email template (`Olá` + hook paragraph + email CTA).

| Route | Shape |
| --- | --- |
| `DIRECT_CALL` | Short opening that identifies CONFENGE, one sanitized public fact, one diagnostic question. No pitch, no unproven legal or economic claim. |
| `ROUTED_CALL` | Same, but asks reception to route. Never greets the switchboard as a personal line. |
| `WHATSAPP` | Accented PT-BR, one contextual fact, one permission question. Well under the 70-word cap. No `objeto:`/`órgão:`/`UF:`/`R$` dump. |
| Generic mailbox | Addresses the observed local-part (`comercial@`, `vendas@`) or "caixa da empresa". Never invents "área de contratos" or a named person. |

Hooks are condensed before interpolation: metadata dumps, US-style amounts, evident OCR, legal-form suffixes, and evidence IDs stay off the copy. The operator card still shows route class, why now, evidence ids, and a **Revisar / copiar** control. There is no auto-send on these cards.

## Inputs (dossier only)

- company (razao/nome/UF)
- contact + role + verification
- service (`service_code` / name / entry offer)
- `why_now` / moment summary
- confirmed + inferred evidence rows (`epistemic_class`)
- internal-structure hypothesis (hypothesis evidence → question only)
- `claims_to_avoid`
- optional touch / reply history
- recent draft bodies (near-dup fingerprints only)

## Output schema

```json
{
  "channel": "EMAIL_INITIAL",
  "subject": "...",
  "body_text": "...",
  "body_html": "",
  "followups": [],
  "fact_used": "...",
  "evidence_ids": ["ev-1"],
  "claims": [{"phrase": "...", "fact": "...", "evidence_ids": ["ev-1"]}],
  "service_code": "ADDITIVE_REVIEW",
  "question": "...",
  "cta": "...",
  "risk_flags": [],
  "rationale": "operator-only; never sent to the lead"
}
```

`body_html` is optional and must be safe (no scripts). The pipeline currently clears model HTML on save unless a future sanitizer is added.

Claims + rationale are packed into `ValidationJSON` (no new migration).

## Deterministic validators

Hard fails include:

- unknown `evidence_id`
- `service_code` mismatch without audited human override
- hypothesis stated as hard fact ("sei que vocês não têm equipe")
- em/en dashes, banned phrases, financial promises, invented urgency
- anti-template / AI clichés / generic subjects / artificial company-name spam
- excessive paragraphs or bullets
- WhatsApp too long or looking like a pasted email
- `generic_cta`: the paragraph carrying the closing question neither states in the first person what the sender does (`trabalho`, `atuo`, `apoio`, ...) nor shares a content word with the observed fact, so the ask fits any company in the corpus
- `personalization_without_fact`: observation language (`Vi que`, `Notei`, `Reparei`, ...) while no observed fact is carried, which asserts something the system cannot evidence

Near-duplicate (character n-gram Jaccard, threshold `0.72`):

- one structure/hook-oriented regeneration is allowed
- if still above the threshold, hard-fail (`validation_ok=false`, never `NEEDS_REVIEW`)
- never loops

`validation_ok=true` means the message is already technically and commercially
sendable. Exact authorization still remains. For ordinary flows this is human;
the separate first-touch CLI uses its own versioned adversarial QA and policy.

## Provider abstraction

`AIDraftGenerator` uses only `generation.Provider.Complete` (no tools, no web search).
`TemplateGenerator` is the offline fallback and still passes the same linters.

## Feature flags

| Variable | Default | Meaning |
| --- | --- | --- |
| `CONFENGE_OUTREACH_ENABLED` | false | Master switch |
| `CONFENGE_REQUIRE_HUMAN_APPROVAL` | true | Keeps global automatic approval disabled |
| `CONFENGE_AUTO_SEND_ENABLED` | false | Fail-closed |
| `CONFENGE_DELEGATED_FIRST_TOUCH_ENABLED` | false | Enables only `CFG-FIRST-TOUCH-ROUTING-v1` |
| `CONFENGE_DELEGATED_FIRST_TOUCH_AUTORUN_ENABLED` | false | Maintains the rolling canonical queue under that policy |
| `CONFENGE_DELEGATED_FIRST_TOUCH_RUNWAY_TARGET` | 100 | Bounds queued+reserved EMAIL scheduling work; grants no transport authority |
| `CONFENGE_MAX_INITIAL_EMAIL_WORDS` | 120 | Hard word cap |
| `CONFENGE_WHATSAPP_ENABLED` | false | WA generate/send |
| `CONFENGE_MAX_WHATSAPP_WORDS` | 70 | WA word cap |

## Non-claims

- Runtime AI may draft. Humans authorize ordinary flows. The delegated worker may
  authorize only the founder-approved first-touch routing policy.
- DNC, opt-out, bounce, and reply remain dominant cadence stops.
- Multi-tenant isolation is enforced by org-scoped repositories.
- Every sent message must remain traceable to lead, contact, channel, text version, approval, and evidence ids.
