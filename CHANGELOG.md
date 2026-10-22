# Changelog

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
