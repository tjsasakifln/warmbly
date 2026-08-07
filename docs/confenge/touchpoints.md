# Per-touchpoint human approval

Every CONFENGE outbound (email or WhatsApp) requires explicit human approval of
that exact message before queue/send. AI never writes `approved_by`.

States: PLANNED → DUE → DRAFTED/NEEDS_REVIEW → APPROVED → QUEUED → SENT.
Terminals: SKIPPED, REJECTED, REPLIED, DNC, BOUNCED, CANCELLED, FAILED.

Transport requires `approved_content_hash == content_hash` and human `approved_by`.
Edit/regenerate clears approval. CAS on APPROVED→QUEUED. Migration `000085`.
Cadence policy `confenge.cadence.v1` (no fixed multi-step campaign sequences).
