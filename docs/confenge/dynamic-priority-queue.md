# CONFENGE dynamic priority queue (Warmbly)

## Split of responsibility

| Layer | Owns |
|-------|------|
| **extra-cli** | Facts, contracts, activation state/score, why-now, hot set |
| **Warmbly** | Import, human approval, DNC, governor, dispatch, operational readiness |

Warmbly **does not** recompute commercial intelligence from contracts.

## Feature flags

| Env | Default | Effect |
|-----|---------|--------|
| `CONFENGE_DYNAMIC_PRIORITY_ENABLED` | `false` | When on, `/app/confenge` work queue uses activation timing |
| `CONFENGE_FEED_SYNC_ENABLED` | `false` | Continuous manifest pull |
| `CONFENGE_EXTRA_CLI_MANIFEST_URL` | empty | HTTPS or `file://` manifest |
| `CONFENGE_FEED_SYNC_INTERVAL` | `15m` | Sync cadence |
| `CONFENGE_EXTRA_CLI_FEED_TOKEN` | empty | Bearer for remote fetch |
| `CONFENGE_EXTRA_CLI_ALLOWED_HOSTS` | empty | Required in prod with remote URL |

Shadow mode: flag off still **imports** activation fields; queue order stays legacy `priority_rank`.

## Activation fields on `outreach_accounts`

Migration `000089_confenge_activation_priority`:

- `activation_state`, `activation_score`, `activation_reason_codes`
- `next_best_action_at`, `activation_expires_at`
- `activation_source_hash`, `message_context_hash`

`queue_state` remains local execution (NEEDS_CONTACT, READY_TO_GENERATE, …).

## Working queue precedence

1. Human-dominant: DNC, blocked, bounce, replied, meeting, proposal, won, lost
2. Active cadence (no duplicate first touch)
3. New outbound when:
   - `activation_state == ACTIONABLE_NOW`
   - `next_best_action_at <= now`
   - not expired
   - no local suppression

Lanes: Needs attention → Agora → Needs contact → Review → Approved → Aguardar.

## Stale message context (release-blocking)

`message_context_hash` covers moment / offer / messaging / evidence / recipient / trigger identity.

It does **not** include pure rank or activation score.

On generate: store `generated_context_hash` on the touchpoint.

On queue / final dispatch:

```text
generated_context_hash == account.message_context_hash
```

Mismatch → fail closed, clear approval, force regenerate + human re-approve.

## Sync

- No SQL access to extra-cli
- SSRF-hardened HTTPS + host allowlist
- Idempotent by snapshot / payload hash
- Partial snapshot never marked success
- Deactivations applied without wiping DNC

## Governor unchanged

`CONFENGE_GLOBAL_SENDS_PER_HOUR=10` remains absolute for email + WhatsApp.
Capacity metrics in the UI are planning only.
