# Commercial Action Cockpit

Warmbly turns extra-cli Decision-Unit + Reachability into controlled commercial
work. It does not discover people, invent identity, or bypass transport gates.

```
extra-cli  WHO + WHY NOW + DECISION UNIT + REACHABILITY
    ↓
Warmbly    what is the next commercial action and approval authority?
    ↓
policy or human executes CALL / ROUTED_CALL / EMAIL / ROLE_EMAIL / WHATSAPP /
                      PROFESSIONAL_SOCIAL / CONTACT_FORM / OTHER
    ↓
OUTCOME + structured feedback upstream
```

Actionability and email sendability are independent. Missing a `VALIDATED`
email is not "no commercial work".

## Email guards

An attributed recipient + messageability `READY` + all hard gates may receive
`DELEGATED_POLICY_APPROVE` only for first touch under
`CFG-FIRST-TOUCH-ROUTING-v1`. Generic, role and public-company freemail routes
must have defensive company attribution and routing CTA. Inferred, stale,
conflicting and blocked recipients go to HOLD/review and never promote through
the policy. Kill switch, dispatch pause, rate limits, stop-on-reply,
suppression, and `ResolveRecipient` stay fail-closed.

## Reachability mapping (`confenge.reachability.v1`)

Warmbly accepts optional additive fields on the current `confenge.outreach.v1`
feed. Unknown fields are ignored. An omitted `reachability_class` is left
empty (current contact-tier contract). An unknown non-empty class maps to
`UNMAPPED` (no auto-send).

| Upstream token | Canonical class | Action | Lane |
| --- | --- | --- | --- |
| `R1`, `R1_DIRECT` | `R1_DIRECT` | `DIRECT_EMAIL` | delegated first touch if every v1 gate passes; otherwise `EMAIL_NEEDS_REVIEW` |
| `R2`, `R2_HIGH_CONFIDENCE_DIRECT`, `INFERRED_DIRECT` | `R2_HIGH_CONFIDENCE_DIRECT` | `INFERRED_EMAIL_REVIEW` | `HUMAN_REVIEW_EMAIL` (never VALIDATED, never dispatch) |
| `R3`, `R3_ROUTED_TO_NAMED_PERSON`, `ROUTES_TO_NAMED_PERSON` | `R3_ROUTED_TO_NAMED_PERSON` | `ROUTED_CALL` | `ROUTED_CALL_QUEUE` |
| `R4`, `R4_ROLE_ROUTE`, `ROLE_MAILBOX` | `R4_ROLE_ROUTE` | `ROLE_EMAIL` | `ROLE_EMAIL_QUEUE` |
| `R5`, `R5_CORPORATE_ONLY` | `R5_CORPORATE_ONLY` | `GENERIC_EMAIL` / `CONTACT_FORM` | `LOW_CONFIDENCE_MANUAL` |
| `R0`, `NO_ACTIONABLE_ROUTE` | `R0_NO_ACTIONABLE_ROUTE` | none | none |
| `BLOCKED`, `DNC` | `BLOCKED` | blocked | `BLOCKED` |

Published extra-cli ActionMode tokens map the same way. `MANUAL_ROUTED_CALL`
is `R3` and is executable without a VALIDATED email. `DIRECT_EMAIL_VALIDATED`
requires VALIDATED + messageability READY plus either an exact human approval
or `DELEGATED_POLICY_APPROVE` for an eligible first touch before
`EmailSendable`. `NAMED_HUMAN_MANUAL_CHANNEL` is a first-class manual lane.
`ROLE_MAILBOX` / `GENERIC` remain manual unless the typed first-touch contract
proves attribution and every policy gate passes. Unknown tokens fail closed to
`UNMAPPED`.

A named person plus a company phone without `BELONGS_TO_NAMED_PERSON` is
`ROUTED_CALL`, never a direct phone.

Suggested copy on CALL, ROUTED_CALL, WhatsApp, and generic/role mailboxes is
route-specific. The founder sees route class, why now, evidence, and a
Revisar / copiar control. Mailbox labels follow the observed local-part
(`comercial@`) or say "caixa da empresa". They never invent "área de contratos".
These cards do not auto-send.

`confenge import` also accepts the extra-cli operator pack (`cards.json`) and
`confenge.decision_unit_account.v1` accounts. Re-import is idempotent. Warmbly
keeps extra-cli account id, person id, evidence ids, why-now, route class,
confidence, recommended action, resolved company domain, and service. Passive
email-verification reports are rendered as operator warnings with DNS, MX,
catch-all, SMTP, and identity-proof dimensions kept separate. `MX_PRESENT`
never becomes mailbox/identity proof, `VERIFIED`, or `email_send_ready=true`.
It does not invent a person, role, email, or phone.

After import the CLI prints an operator summary: actionable accounts, route
distribution, manual-call count, email-safe count, unresolved blockers, and
the next human actions.

## Plug contract for extra-cli (additive)

Keep shipping `confenge.outreach.v1`. Optionally add, per lead or contact:

```json
{
  "decision_unit_candidates": [],
  "reachability_routes": [],
  "recommended_target": {},
  "recommended_route": {},
  "recommended_action": "ROUTED_CALL",
  "contacts": [{
    "reachability_class": "R3_ROUTED_TO_NAMED_PERSON",
    "route_type": "phone",
    "route_relation": "ROUTES_TO_NAMED_PERSON",
    "channel_value": "+554132220000",
    "channel_display": "telefone oficial da empresa",
    "inferred_email": false,
    "verification_status": "CANDIDATE_UNVERIFIED",
    "email_send_ready": false
  }]
}
```

Operator cards may also publish `domain_resolution`, `email_verification`, and
`email_verification_reports`. These are visibility/provenance fields, not a
send authorization. `CANDIDATE_UNVERIFIED` and `INFERRED` stay on
`HUMAN_REVIEW_EMAIL` / `NEEDS_ENRICHMENT`. They never enter send `NEEDS_REVIEW`.
Legacy cards remain compatible and continue through the same recipient,
messageability, approval, and transport gates.

Do not invent a person when only a role is known. Warmbly will target the
function, not a fabricated name.

## Outcomes back to extra-cli

`confenge.outcome.v1` is extended additively. New top-level fields (optional):

- `action_id`, `action_type`, `reachability_class`, `outcome_code`
- `target_reached`, `conversation_started`, `interest_state`
- `person_relevance_feedback`, `route_validity`
- `referral` `{name, role, followup_action_id}`
- `new_person`, `new_role`, `new_route`, `preferred_channel`

WON is never inferred from `INTERESTED` or `MEETING_SCHEDULED`.

## Lanes

`EMAIL_NEEDS_REVIEW`, `HUMAN_REVIEW_EMAIL`, `CALL_QUEUE`, `ROUTED_CALL_QUEUE`,
`WHATSAPP_QUEUE`, `PROFESSIONAL_SOCIAL_QUEUE`, `ROLE_EMAIL_QUEUE`,
`CONTACT_FORM_QUEUE`, `LOW_CONFIDENCE_MANUAL`, `NEEDS_ENRICHMENT`,
`INBOUND_NOW`, `BLOCKED`, `DONE`.

`INBOUND_NOW` is the Monday work queue for web-cfg receipts. See
[inbound-ingest.md](./inbound-ingest.md).

## What this version does not do

No dialer, WhatsApp bot, LinkedIn bot, form submitter, CRM, calendar, or
forecasting. No person discovery inside Warmbly.
