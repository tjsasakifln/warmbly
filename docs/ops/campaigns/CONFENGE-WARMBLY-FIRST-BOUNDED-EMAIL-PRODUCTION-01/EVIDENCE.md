# CONFENGE first bounded email cohort — production evidence

Round: `CONFENGE-WARMBLY-FIRST-BOUNDED-EMAIL-PRODUCTION-01` (2026-08-22 UTC)

## Verdict

`PRODUCTION_BLOCKED_BY_EXTERNAL_GATE:SEND_WINDOW`

`GO_FOR_CONTROLLED_EMAIL_PILOT` was emitted by the live evaluator on the deployed SHA with
every required gate PASS. No message was dispatched: the authorized 24h TTL and the campaign
send window do not intersect, so no real send was possible inside the authorization.

## Release identity

| Field | Value |
| --- | --- |
| PRODUCTION_SHA | `8fa1af2cff88900013a683528f22935d33a57764` |
| MAIN_SHA | `8fa1af2cff88900013a683528f22935d33a57764` |
| Migration version | `110` (not dirty) |
| Feed schema | `confenge.outreach.v1` |
| Feed identity | `fresh-cohort-20260822T030709Z` |
| FEED_HASH (sha256 of feed document) | `d71ce315d94d3846e6d84e9b5dd075de14e034c95b6111fe138e67767f3c9dca` |
| COHORT_ID | `controlled-f02294cb6a1f` |
| COHORT_HASH | `f02294cb6a1f55a7c947d111128f0c683d0319ef9bd6fb24c997c6059165ebd0` |
| RECIPIENT_SET_HASH | `f3f1095d4d9c525768fa8585d2d5eaf3020b4969b473d9e2e65d8a9a486a8e0b` |
| AUTHORIZATION_ID | `a8bb9e08-8c60-45d1-8eae-8f00e502d275` |
| FROZEN_HASH | `464bc6c684437d3e466e1dce06f45e3a0a61dfc47c1ac6e2ce35aa9d03a1bab7` |
| POLICY_VERSION | `bounded-cohort-policy.v1` |
| COMPOSER_VERSION | `confenge.composer.v3` |
| EVIDENCE_VERSION | `controlled-email-policy.v1` |
| MAX_DAILY | `50` |
| TTL | `24h`, expires `2026-08-23T04:25:13Z` |
| Human verdict | `READY_FOR_CONTROLLED_EMAIL_GO_REVIEW` |
| Reason | `founder_authorized_final_production_round_2026-08-21` |

## Cohort composition

Counts only. The frozen manifest carries operational PII and stays on the host volume,
never in git.

| Metric | Value |
| --- | --- |
| accounts_considered | 50 |
| accounts_eligible | 50 |
| recipients_final | 50 |
| unique mailboxes | 50 |
| unique accounts | 50 (exactly one initial route per account) |
| GENERIC_COMPANY | 38 |
| ROLE_OR_DEPARTMENT | 12 |
| PROBABILISTIC_OR_RISKY | 0 (excluded by policy) |
| suppressed / opt_out / hard_bounce | 0 / 0 / 0 |
| duplicates / missing_provenance / stale | 0 / 0 / 0 |
| copy_qa_failures | 0 |
| person_unknown | 50/50 (no invented person, role, or title) |

## Live GO review — every required gate

| Check | State |
| --- | --- |
| repository_sha | PASS (expected == live) |
| schema | PASS |
| feed_hash | PASS |
| cohort_hash | PASS |
| recipient_set_hash | PASS |
| policy_version | PASS |
| composer_version | PASS |
| evidence_version | PASS |
| route_classes | PASS |
| volume_cap | PASS (50) |
| smtp_ready | PASS |
| reply_ingest_ready | PASS |
| observability_ready | PASS |
| dispatch_wiring | PASS |
| sender_provider_config | PASS |
| db_cohort_authority | PASS |
| suppression_clear | PASS |
| ttl_valid | PASS |
| kill_switch_available | PASS |
| sending_paused | false |
| auto_send | false |
| green_autorun | false |

`release_verdict=GO_FOR_CONTROLLED_EMAIL_PILOT`

## Reply ingest — resolved

IMAP is the real mechanism, not an assumption. Replies arrive worker IMAP sync ->
consumer `ProcessIncomingReply` -> `OnClassifiedReply`. Proven live read-only:
TCP reachability to the campaign IMAP host from both the VPS host and the worker
container, plus an active `smtp_imap` mailbox row with a non-empty `imap_host`.
No mailbox content was fetched and no credential was logged or read.

## Transport reachability

Outbound submission and IMAP are open from the host and from inside the worker
container (587, 465, 993). The Netcup outbound-SMTP unlock is in place.

## The blocker

The CONFENGE campaign shell sends Mon-Fri 09:00-18:00 `America/Sao_Paulo`
(`days` bitmask 31). `ListCampaignScheduleCandidates` additionally requires
`campaigns.status = 'active'`; the shell is `draft`.

| Moment | Value |
| --- | --- |
| Review executed | Sat 2026-08-22 01:26 (-03) |
| Grant TTL expiry | Sun 2026-08-23 01:25 (-03) |
| Next send window opens | Mon 2026-08-24 09:00 (-03) |
| Gap | window opens **31.6 h after the grant expires** |

The 24h TTL cannot reach a send window, so no dispatch inside this authorization
could have produced a real email.

Dispatching anyway was rejected deliberately: it would have enrolled 50 contacts into a
campaign that never runs, consumed all 50 daily-cap slots, and moved 50 touchpoints to
`QUEUED` — which the `route_already_dispatched` guard would then block from
re-authorization. That is strictly worse than not dispatching.

## Post-state (verified)

| Metric | Value |
| --- | --- |
| REAL_EMAIL_SENT | false |
| N_ATTEMPTED | 0 |
| N_PROVIDER_ACCEPTED | 0 |
| campaign_leads | 0 |
| contacts | 0 |
| touchpoints QUEUED/SENT | 0 |
| cohort slots consumed | 0 |
| touchpoints APPROVED under the grant | 50 |
| grant revoked | no (valid until TTL) |
| AUTO_SEND_ENABLED | false |
| GREEN_AUTORUN_ENABLED | false |
| KILL_SWITCH_AVAILABLE | true (engaged, fail-closed) |

## Defects found and fixed this round

Three shipped defects made a live GO structurally unreachable before this round.

1. **#111** — a cohort frozen from the feed had no `account_id`/`candidate_id` (cannot
   dispatch); one frozen from Postgres had no `feed_identity` (live review reports
   `feed_identity_missing`). Neither shape could both pass review and dispatch.
2. **#112** — one stale `CANCELLED` touchpoint from an older composer version blocked the
   whole 50-member cohort, because authorization refuses partial apply.
3. **#113 / #114** — two schema blockers: `authorization_mode` had no
   `BOUNDED_COHORT_AUTHORIZATION` value, and the grant column carried a foreign key to the
   campaign-policy table only. Every bounded-cohort touchpoint write failed against a real
   database, on every SHA since #104. Unit tests missed both because the in-memory
   repository has no constraints.

## Smallest human action to complete the round

Either is one decision, not new engineering:

**A. Run inside the window (recommended).** On Mon 2026-08-24 between 09:00 and 18:00
`America/Sao_Paulo`, activate the campaign shell and re-run the sequence in `RUNBOOK.md`.
A fresh 24h grant is required because this one expires Sunday.

**B. Authorize sending outside business days.** Widen the campaign `days`/window. This
sends cold B2B mail outside business hours and is worse for deliverability, especially
with `auth_dkim = false` on the sending mailbox.

## Deliverability note (unrelated to the blocker)

The sending mailbox reports `auth_spf = true`, `auth_dmarc = true`, `auth_dkim = false`.
DKIM should be fixed before the first real cold cohort. Tracked separately by #138.

Copy QA passes every policy gate, but three cosmetic defects are worth fixing before real
recipients see them: subjects truncate mid-word, the body embeds a raw record dump
(`objeto: ...; órgão: ...; UF ...; R$ ...`), and the English service code `portfolio review`
leaks into Portuguese copy.
