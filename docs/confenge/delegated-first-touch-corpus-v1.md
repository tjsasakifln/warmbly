# Delegated first-touch corpus report v1

Date: 2026-08-26

Rules: `confenge.first-touch-copy-rules.v1`

Composer: `confenge.composer.v7`

## Decision

The delegated composer no longer rotates a bank of interchangeable subjects,
openings, purpose sentences and questions. It builds one typed brief from the
current company, confirmed supplier role, supported public fact, service code,
route class and recipient purpose. A missing specific fact produces a shorter
supplier-role message. It never causes an invented detail.

The v1 corpus rules make the following editorial distinctions:

- Exact subject and body reuse is a hard failure after the first occurrence.
  This rule has the same meaning for 10 or 10,000 messages: two evidence briefs
  must not produce the same complete reader-facing message.
- A near duplicate is the same factual focus, service practice, route class and
  recipient purpose after removing company and person identity. It is measured
  as a semantic group. It is not failed against an unexplained percentage.
- Subject, opening, practice-line and CTA concentration are reported with the
  evidence dimension that supports each repetition. Practice follows
  `service_code`; CTA follows route class plus recipient purpose. Concentration
  alone does not trigger synonym rotation.
- Unsupported claims, buyer/supplier confusion, guessed people, internal
  metadata, offensive or manipulative language, empty content, wrong-route CTA
  and missing contact exit have a required count of zero.
- The complete body remains inside the existing 45 to 150 word hard band.

This preserves the decision in issue #152. A practice line covering 47% of a
feed is legitimate when exactly 47% of the feed has the supporting service. It
would be a defect if the line crossed service boundaries or if cosmetic wording
were used to hide the concentration.

## Corpus construction

Both corpora are deterministic and generated in the Go regression suite.

- The sanitized replay contributes 200 structural seeds from
  `data/confenge-feeds/full_national/chunk_*.json`, the same 200-lead source used
  by issue #152. Sanitization keeps only service code, moment code and contract
  object shape. Company, contact, agency, value and record identifiers are
  discarded.
- The remaining records are structured synthetic briefs with unique companies
  and confirmed contract objects. The service distribution is the measured
  #152 distribution: 47% budget/spreadsheet audit, 32% contract monitoring, 11%
  backoffice, 9% diagnostic and 1% addenda.
- Routes are balanced across `DIRECT_PERSON`, `ROLE_OR_DEPARTMENT`,
  `GENERIC_COMPANY` and `PUBLIC_COMPANY_FREEMAIL`. Only direct routes with
  person evidence use a first name.
- Every specific fact is present in a `CONFIRMED_FACT` evidence row. Replay
  objects that are numeric, truncated, metadata-shaped or not supportable fall
  back to the simple supplier-role copy.

## Results

| Metric | 1,000 | 10,000 |
| --- | ---: | ---: |
| Exact duplicate groups / messages | 0 / 0 | 0 / 0 |
| Near-duplicate groups / messages | 13 / 111 | 13 / 111 |
| Largest near-duplicate group | 19 | 19 |
| Largest exact subject concentration | 1 (0.10%) | 1 (0.01%) |
| Specific-fact opening | 887 (88.70%) | 9,887 (98.87%) |
| Simple supplier-role fallback | 113 (11.30%) | 113 (1.13%) |
| Largest practice line | 470 (47.00%) | 4,700 (47.00%) |
| Generic/freemail forwarding CTA | 500 (50.00%) | 5,000 (50.00%) |
| Direct-person ownership CTA | 250 (25.00%) | 2,500 (25.00%) |
| Role/purpose area CTA | 250 (25.00%) | 2,500 (25.00%) |
| Unsupported claims | 0 | 0 |
| Buyer/supplier confusion | 0 | 0 |
| Guessed people | 0 | 0 |
| Internal metadata leakage | 0 | 0 |
| Offensive or manipulative language | 0 | 0 |
| Empty subject or body | 0 | 0 |
| Route-inappropriate CTA | 0 | 0 |
| Missing contact exit | 0 | 0 |
| Word length min / average / p50 | 49 / 56.51 / 56 | 49 / 56.21 / 56 |
| Word length p90 / p95 / p99 / max | 60 / 60 / 65 / 69 | 58 / 60 / 60 / 69 |

The 111 near-duplicate messages all come from 13 sanitized replay groups whose
specific object could not safely enter copy. They use the explicit simple
fallback and remain visible in the report. The 9,800 synthetic briefs add
supported factual focus without increasing those groups.

The local 10,000-message regression completed composition, a second canonical
projection check, aggregation, sorting and JSON serialization in 31.04 seconds.
This is a corpus-test measurement, not a production latency threshold. The
worker composes one item through the O(1) path, while the audit is O(n log n) and
performs no all-pairs comparison. No model call is present in either path.

## Reproduction

```bash
go test ./internal/app/confenge \
  -run '^TestDelegatedFirstTouchCorpusAtScale$' -count=1 -v
```

The test prints the complete JSON report for both sizes. Adversarial regressions
also prove deterministic failures for unsupported claims, contracting-authority
inversion, exact content reuse, empty content, hallucinated people, internal
metadata, offensive or manipulative wording and route-inappropriate CTAs.
