# CONFENGE 10k no-SMTP Warmbly rehearsal, 2026-08-26

Evidence scope: historical, evidence-only result from PR #207 at
`1f51817b`. It motivated and is now superseded by the current-main composition.
It is not evidence that the combined release SHA passed this rehearsal.

Status: **PASS for synthetic ingest, materialization, scheduling, refresh, and
recovery**. Dispatch was never invoked, no application service was started,
the fixture's SMTP configuration pointed to the non-routable `smtp.invalid`
host, and `SENT` remained zero. This is not transport
GO and does not establish a send rate.

The sanitized machine-readable evidence is
[`10k-no-smtp-rehearsal-2026-08-26.json`](10k-no-smtp-rehearsal-2026-08-26.json).

## Final closure

| Stage | v1 | v2 after 10% refresh |
|---|---:|---:|
| Imported/current | 10,000 | 10,000 |
| Supplier confirmed | 9,000 | 9,000 |
| Candidate attributed | 6,000 | 6,000 |
| INITIAL prepared | 6,000 | 5,999 |
| Delegated eligible | 5,000 | 4,999 |
| Held with reason | 5,000 | 5,001 |

The one-account difference in v2 is the injected suppression arriving before
`due_at`; suppression deliberately wins over scheduling.

Final state: 10,000 current accounts, 9,000 candidates, 10,000 evidence rows,
5,999 source-bound INITIAL touchpoints, 100 live queue rows, 100 cancelled stale
queue rows, zero stale transportable rows, zero duplicate intent groups, zero
orphans without a reason, zero import errors, and zero `SENT` touchpoints.

## Fault and restart sequence

1. The importer was cancelled after persisting part of a chunk. It left a
   durable `running` import; a reconstructed service reclaimed it after the
   stale threshold. The final audit found one
   `resumed_stale_running_import` retry and 10,000 current accounts.
2. A PostgreSQL backend was terminated. The pool recovered before scheduling
   continued.
3. The runway was filled to 50, the service/worker was reconstructed, and the
   target was raised and converged to 100.
4. A queued decision's policy hash was changed; stale retirement removed it.
   Mailbox capacity was changed to unknown and the transport gate blocked.
   A suppression arriving before `due_at` also blocked transport.
5. The v2 source run replaced exactly 1,000 accounts. The first producer
   contract used unsupported `NOT_ACTIONABLE`; Warmbly rejected it and retained
   the v1 snapshot as `partial`. After the owner fix to `SUPPRESSED`, retry from
   the same database state committed v2, retired stale work, and replenished the
   runway.
6. The preserved test tenant had lost its synthetic worker during teardown;
   canonical mailbox validation failed closed. Restoring that isolated fixture
   dependency allowed scheduling to continue. No production guard changed.
7. A final audit query initially assumed import `errors` was always a JSON
   array. JSON `null` exposed the harness bug; the metric now counts arrays
   defensively. The operational data was unchanged.

Every failure was visible and stopped progress. None produced a silent partial
success or transportable stale intent.

## Throughput and resources

The current-run decisions span 607.000 s from first to last; 100 queue slots
were filled, or 0.165 queue positions/s on this shared development host.
PostgreSQL showed no deadlock, no ungranted lock at sampled/final checks, no temp
file, and no busy loop once the runway was full. Ten full-runway polls completed
idle in 0.072 s.

Observed PostgreSQL memory peaked near 157.5 MiB and settled near 131.3 MiB.
Container block I/O ended at approximately 124 MB read / 1.6 GB written. The
largest timed Go command used 709,852 KiB RSS (including test/build execution);
the final in-test process high-water mark was 37,936 KiB.

The dominant bottleneck was canonical per-lead evaluation/persistence, not
locks or memory growth. Feed payloads are now validated into bounded temporary
files rather than retained together in memory, and the current-run INITIAL
backlog is classified/materialized set-wise in PostgreSQL. Further throughput
work should optimize the existing persistence path; it must not add a parallel
queue or weaken copy, evidence, authorization, governor, or transport gates.

These figures estimate scheduling headroom only. They must not be converted
into SMTP throughput, mailbox capacity, or a promised delivery rate.

## Reproduction

The opt-in PostgreSQL tests require a fully migrated disposable database and
the deterministic extra-cli manifests:

```bash
WARMBLY_TEST_POSTGRES_DSN='postgresql://…' \
WARMBLY_REHEARSAL_DISPOSABLE_DB=1 \
WARMBLY_REHEARSAL_MANIFEST_V1='/tmp/confenge-extra-10k/feed-v1/manifest.json' \
WARMBLY_REHEARSAL_MANIFEST_V2='/tmp/confenge-extra-10k/feed-v2/manifest.json' \
WARMBLY_REHEARSAL_REPORT_PATH='/tmp/warmbly-confenge-10k-report.json' \
go test ./internal/app/confenge \
  -run '^TestConfengeTenThousandNoSMTPRehearsal$' -count=1 -v -timeout=55m
```

`TestConfengeTenThousandNoSMTPRefreshRecovery` is the opt-in continuation for a
tenant deliberately preserved in the partial refresh state. Neither test calls
`ProcessDispatchQueueOnce` or any provider transport. The current-main test
also requires producer-owned source expiry and complete membership attestation,
and reads back zero provider attempts, zero provider acceptances, and zero sent
touchpoints from PostgreSQL.
