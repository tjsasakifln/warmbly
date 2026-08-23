# Controlled email cohort operator

The founder path for the first real cohort is:

```text
confenge import --feed PATH
confenge cohort prepare --feed PATH --org-id ORG_UUID --out /tmp/cohort.json
confenge cohort preview --manifest /tmp/cohort.json
confenge cohort authorize --manifest /tmp/cohort.json --actor UUID
confenge cohort authorize --manifest /tmp/cohort.json --actor UUID --confirm
confenge cohort review --id AUTHORIZATION_UUID --actor UUID
confenge cohort review --id AUTHORIZATION_UUID --actor UUID --verdict READY_FOR_CONTROLLED_EMAIL_GO_REVIEW --confirm
confenge cohort dispatch --id AUTHORIZATION_UUID --actor UUID --limit 10 --confirm
confenge cohort report --events PATH
```

Hashes (`cohort_id`, `cohort_hash`, `recipient_set_hash`, SHA, feed identity, policy/composer/evidence versions) are derived by `prepare`. Do not copy them by hand.

The live extra-cli feed must also carry an unexpired
`PNCP_CONTRACT_FRESHNESS/1.0` attestation. Production `prepare`, `authorize`,
live GO and final transport all revalidate it. The attestation is bound into
the cohort hash, so removing or changing it after freeze is a hard block.

Pass `--feed` and `--org-id` together for a cohort you intend to dispatch. The feed supplies identity and scopes the freeze to that import run; Postgres supplies the real account and candidate ids the dispatch path needs. `--feed` alone freezes straight from the document and leaves those ids empty, so it previews but cannot dispatch. `--org-id` alone carries no feed identity, so the live GO review reports `feed_identity_missing` and never reaches `GO_FOR_CONTROLLED_EMAIL_PILOT`.

`cohort-auth create|show|revoke` remains the low-level grant primitive.

## What prepare does

- One initial route per account
- Honors `preferred_initial`; a proven `DIRECT_PERSON` may win
- Allows `ROLE_OR_DEPARTMENT`, `GENERIC_COMPANY`, `PUBLIC_COMPANY_FREEMAIL`
- Keeps `PROBABILISTIC_OR_RISKY` out of the default pilot
- Excludes hard bounce, opt-out, suppression, DNC, stale evidence, missing provenance, shotgun duplicates
- Extra-cli `route_suppression: NONE` is not suppression. `provenance_chain_valid: false` does not by itself drop a contact extra-cli already stamped `controlled_email_eligible`. Demo or fixture taint still excludes.
- Absence of a named person does **not** exclude a control-eligible route
- Prints a reconciling preview (eligible + excluded = considered)

## Authorization

`--confirm` persists the grant in PostgreSQL with the frozen membership JSON and applies `BOUNDED_COHORT_AUTHORIZATION` to the exact touchpoints. Partial apply is refused and the grant is revoked.

This is not auto-send. `CONFENGE_AUTO_SEND_ENABLED` stays `false`. GREEN autorun stays off.

## GO review

`cohort review` loads the persisted grant, collects a live release manifest from the running system (deployed SHA, SMTP TCP reachability, kill-switch file, auto-send/GREEN flags, PostgreSQL grant/membership, suppression, TTL, observe-path wiring), and prints PASS/FAIL/UNKNOWN per check. Missing live evidence is `NO_GO`. The CLI never treats an empty manifest as ready.

`--confirm` persists the human decision against that live manifest. `READY_FOR_CONTROLLED_EMAIL_GO_REVIEW` is the human verdict. The evaluator emits `GO_FOR_CONTROLLED_EMAIL_PILOT` only when every required live check is PASS, including SMTP and IMAP reply-ingest (UNKNOWN is NO_GO). Drift, RISKY, auto-send, GREEN autorun, an engaged kill switch, or post-freeze suppression is `NO_GO`.

`confenge cohort dispatch --id UUID --actor UUID --confirm` is the bounded send
for the first real experiment (`N<=10`, `max_daily<=10`). Authorization and
dispatch reject larger values. Global auto-send stays false. The kill switch
remains available.

## Observability

Send, bounce, and reply paths snapshot `email_route_class`, `cohort_id`,
`policy_version`, canonical account and touchpoint ids, and provider onto
commercial events. SMTP acceptance is separate from attempted. HARD and SOFT
bounces remain distinct; only definitive HARD bounce suppresses the mailbox.
Replies are correlated first through RFC message identifiers and persisted
touchpoint relationships. No-reply and unproved delivery stay UNKNOWN. SMTP
`250 accepted` is never projected as delivered. Reply is not a meeting.

## Safety

- Tests never open a real SMTP session or send mail
- `AUTO_SEND_ENABLED=false`
- Kill switch still blocks before transport
- Daily cap is reserved in PostgreSQL before SMTP
- Live `cohort dispatch` records attempted vs provider-accepted; delivered is not inferred
