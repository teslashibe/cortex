# Changelog

## v1.4.0

- Add an optional evidence reader with bounded query planning, source-checked quotation extraction, cited answer composition, and one validated format repair.
- Improve evidence diversity with an additional lexical channel, simple plural and month aliases, speaker preference, and per-source limits.
- Calculate civil-day and completed-calendar-month differences from source-bound date expressions; expose event-count inputs separately from model judgments.
- Recheck source eligibility after answer generation and discard partial results when evidence changes.
- Preserve nonempty whitespace-only source windows during embedding so one such excerpt cannot interrupt indexing.
- Expand private conversation evaluation and project-separated provisional classifier training. The classifier remains advisory.
- Publish a versioned academic manuscript connecting the design to complementary learning systems and sleep research, with measured results and explicit limits.

## v1.3.0

- Add optional exact-tokenizer source segmentation with full-coverage validation and separate vector namespaces.
- Add a local ONNX adapter that rejects oversized inputs instead of silently truncating them.
- Enforce the classifier's complete input budget in training and serving, with versioned context formatting. Flag incomplete teacher annotations for review.
- Add bounded multi-query evidence retrieval with deduplicated windows and explicit coverage limits.
- Resolve supported date expressions from exact source spans and explicit timezones; provide civil-day subtraction.
- Expand private, project-separated classification and frozen public retrieval diagnostics. Classifiers remain advisory.
- Publish a revised academic paper with the new contracts, conditional proofs, measured results, and remaining limits.

## v1.2.0

- Add opt-in source-window indexing and recall with exact offsets and attribution.
- Publish a complete observation's vectors atomically; recheck owner and lifecycle eligibility after provider work.
- Add a shadow classifier interface and HTTP adapter separating retention from memory kind. Classification never mutates memories.
- Add private labeling, baseline/SetFit training, group-split validation, calibration metrics, and local serving tools.
- Replace the current research presentation with an academic manuscript, including conditional proofs and an audited evaluation record. Preserve historical papers and link their grading erratum.

## v1.1.0

- An explicit `fact` kind preserves statements that do not fit a preference, routine, relationship, behavior or project.
- Summaries may contain up to 512 characters to preserve qualifications in project details.
- Consolidation reviews multiple facts in one observation and reports unreferenced observation IDs for audit.
- Opt-in `RecallObservations` and `IndexObservations` preserve source content, speaker metadata and timestamps. Existing memory recall and indexing remain unchanged.
- Source retrieval excludes evidence of superseded, archived or expired memories and rechecks eligibility after provider work.
- `ForgetObservation` removes unretained evidence, derived memories and exact-content reimports through the existing forgetting barrier.
- One bounded format repair handles invalid successful model responses; transport errors are not automatically retried. Validation errors identify the failing field without echoing private memory content.
- Migration 005 adds cascading observation embeddings and a source lookup index. Upgrade all writers before using the new memory kind.

## v1.0.1

- Optional semantic embeddings and hybrid rank fusion, with bounded exact vector search in PostgreSQL-backed memory sets.
- Relevance decay separate from evidence confidence; explicit maintenance, pinning, expiry, and restoration.
- Forgetting removes connected memory versions, shared source observations, embeddings, and feedback. Hash tombstones reject exact reimports.
- Retry-safe explicit feedback updates a small utility estimate without model training.
- Source evidence timestamps and resolved inference model are retained.
- Consolidation selects at most 500 relevant memories and reports when context is limited.
- Default inference model is `gpt-6-luna-medium` through Open Agent API.
- Migration 004 is additive. Upgrade every writer before using maintenance or forgetting; older binaries do not enforce the new policies.

## v1.0.0

First public release of Cortex under the MIT license.

- Go library with `Observe`, `Recall`, and `Consolidate` operations.
- PostgreSQL storage with embedded migrations and user-scoped observation idempotency.
- Strict model-output validation, source provenance, and retained contradiction history.
- Atomic memory changes, exact observation acknowledgement, and recovery after interrupted consolidation.
- Replaceable model interface and an HTTP client for the documented Open Agent API setup.
- Local HTTP service, export import commands, and synthetic examples.
- Automated PostgreSQL regression tests, race checks, vulnerability checks, and secret scanning.
- Requires Go 1.26.10 or newer, including the patched certificate verifier.

Cortex's HTTP service trusts local callers. Applications own authentication,
authorization, and source consent. Recall uses text matching; there is no vector
search, background scheduler, or automatic source-permission revocation.
