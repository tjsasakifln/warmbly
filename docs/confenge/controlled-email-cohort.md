# Controlled email cohort operator

The founder path for the first real cohort is:

```text
confenge import --feed PATH
confenge cohort prepare --feed PATH --out /tmp/cohort.json
confenge cohort preview --manifest /tmp/cohort.json
confenge cohort authorize --manifest /tmp/cohort.json --actor UUID
confenge cohort authorize --manifest /tmp/cohort.json --actor UUID --confirm
confenge cohort review --id AUTHORIZATION_UUID --actor UUID --verdict READY_FOR_CONTROLLED_EMAIL_GO_REVIEW --confirm
confenge cohort report --events PATH
```

Hashes (`cohort_id`, `cohort_hash`, `recipient_set_hash`, SHA, feed identity, policy/composer/evidence versions) are derived by `prepare`. Do not copy them by hand.

`cohort-auth create|show|revoke` remains the low-level grant primitive.

## What prepare does

- One initial route per account
- Honors `preferred_initial`; a proven `DIRECT_PERSON` may win
- Allows `ROLE_OR_DEPARTMENT`, `GENERIC_COMPANY`, `PUBLIC_COMPANY_FREEMAIL`
- Keeps `PROBABILISTIC_OR_RISKY` out of the default pilot
- Excludes hard bounce, opt-out, suppression, DNC, stale evidence, missing provenance, shotgun duplicates
- Absence of a named person does **not** exclude a control-eligible route
- Prints a reconciling preview (eligible + excluded = considered)

## Authorization

`--confirm` persists the grant in PostgreSQL with the frozen membership JSON and applies `BOUNDED_COHORT_AUTHORIZATION` to the exact touchpoints. Partial apply is refused and the grant is revoked.

This is not auto-send. `CONFENGE_AUTO_SEND_ENABLED` stays `false`. GREEN autorun stays off.

## GO review

`READY_FOR_CONTROLLED_EMAIL_GO_REVIEW` is not `GO_FOR_CONTROLLED_EMAIL_PILOT`. The human verdict is limited to that SHA, feed, cohort, recipient set, policy, classes, cap, TTL, composer, and evidence version. Drift or RISKY is `NO_GO`.

## Observability

Send, bounce, and reply paths snapshot `email_route_class`, `cohort_id`, `policy_version`, and provider onto commercial events. No-reply stays UNKNOWN. Delivered is not a decision-maker. Reply is not a meeting.

## Safety

- `REAL_EMAIL_SENT=false` for this operator and its tests
- `AUTO_SEND_ENABLED=false`
- Kill switch still blocks before transport
- Daily cap is reserved in PostgreSQL before SMTP
