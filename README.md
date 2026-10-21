# Cortex

A small PostgreSQL memory engine: `Observe`, `Recall`, and `Consolidate`.
Inference uses the provider-neutral `llm` package and a normal, non-streaming
OpenAI-compatible `/chat/completions` endpoint. The default target is
[Open Agent API](https://github.com/teslashibe/open-agent-api) with `gpt-6-luna-medium`.

## Install

Use the tagged Go package:

```sh
go get github.com/teslashibe/cortex@v1.0.1
```

Install the commands with Go:

```sh
go install github.com/teslashibe/cortex/cmd/cortex@v1.0.1
go install github.com/teslashibe/cortex/cmd/cortex-import@v1.0.1
go install github.com/teslashibe/cortex/cmd/cortex-once@v1.0.1
```

Prebuilt command archives and checksums are available from the
[v1.0.1 release](https://github.com/teslashibe/cortex/releases/tag/v1.0.1).
For the source examples below, clone this repository and run the commands from
its root directory.

## Run

Requires Go 1.26.10 or newer and PostgreSQL with permission to install `pg_trgm`.

```bash
export DATABASE_URL='postgres://user:pass@localhost/cortex?sslmode=disable'
export LLM_BASE_URL='http://127.0.0.1:8088/v1'
# Set LLM_API_KEY if your gateway requires authentication.
go run ./cmd/cortex
```

The API binds to **127.0.0.1:8090** by default. It is a **trusted-localhost API**:
it does not authenticate callers or authorize the requested `user_id`. Database
mutations are scoped by user, but that is not an authentication boundary. Do not
expose the listener to an untrusted network without an authenticating proxy.

The profile tier filter is a display heuristic, not a privacy or authorization
boundary. A trusted caller can request the internal tier or recall unfiltered
memories. The embedding application must enforce access and sharing decisions.

Configuration:

- `DATABASE_URL`: required PostgreSQL connection string.
- `LLM_BASE_URL`: defaults to `http://127.0.0.1:8088/v1`.
- `LLM_API_KEY`: optional bearer token; required only if the gateway requires it.
- `CORTEX_MODEL`: defaults to `gpt-6-luna-medium`.
- `CORTEX_LISTEN`: defaults to `127.0.0.1:8090`.

There are no Anthropic keys, effort knobs, batch submit/poll workers, or automatic
consolidation/decay cron jobs. The application schedules consolidation and maintenance. Model requests have a two-minute timeout and a
complete consolidation request has a 30-minute timeout. The adapter sends
`faithful: false` and `prewarm: false`, which are Open Agent API
extensions. Check support before using another chat endpoint. There are no
automatic model retries.

## Import and consolidate

The existing `cortex-once` command name is retained. It optionally imports a
Claude export, synchronously consolidates pending observations, and prints a
profile. The export format is independent of the inference provider.

```bash
go run ./cmd/cortex-once --dir /path/to/claude-export --user demo-user --tier internal
# Consolidate already-imported observations without another import:
go run ./cmd/cortex-once --user demo-user --chunk 100 --by-day
```

`--chunk` is a maximum observation count (1–200, default 200). `--by-day` keeps UTC
days separate while preserving chronological processing. Each chunk sees the
memories committed by earlier chunks. `--resume`, polling, and batch-specific
flags are removed; rerun the same command to process whatever remains pending.
`cortex-import` remains available for importing through the HTTP API. Both import
commands default to the `default` user scope; pass `--user` explicitly when
keeping separate histories.

## HTTP

- `POST /v1/observations`: JSON `{user_id, observations: [...]}`.
- `GET /v1/recall?user=&q=&limit=`: ranked, current memories; no automatic feedback writes.
- `POST /v1/consolidate?user=`: synchronous consolidation, returning saved run IDs
  and counts. An empty backlog returns `status: "noop"`; a concurrent request for
  the same user returns HTTP 409. Partial failure includes already-completed runs
  and the failed run in `result`.
- `POST /v1/dream?user=`: compatibility alias for the same synchronous handler.
  The old batch response and `force` semantics are not retained.
- `GET /v1/profile/{user}?tier=public|internal`: Markdown, or JSON when requested
  with `Accept: application/json`.
- `POST /v1/signals`: explicit feedback, with user-scoped memory validation.
- `GET /healthz`: health and embedded prompt version.

## Consolidation safety and semantics

A PostgreSQL **connection advisory lock** serializes consolidation per user across
processes. The locked connection remains reserved during inference, but no
transaction remains open during a network call. This requires a direct PostgreSQL
connection or session-pooling proxy; transaction-pooling proxies are not supported.
The lock releases automatically if the database connection dies. Hash collisions
can conservatively serialize two unrelated users, never mix their data.

The `consolidations` table records each chunk's exact observation IDs, model,
prompt-content hash, status, counts, and error. Applying all operations,
acknowledging those exact observation IDs, and completing the run happens in one
transaction. Re-completing an already completed run is a no-op. An explicit empty
`ops` array is a valid completion; malformed envelopes, unknown fields, duplicate
JSON keys, nulls, invalid IDs, or invalid confidence values reject the whole
chunk. Fact mutations require `sources` from that chunk; memory references must
belong to the same user and still be current.

A crash before commit leaves observations pending. On the next request, after
acquiring the user lock, an interrupted running row is marked failed and its
observations are eligible again. A retry can make another inference request, but
cannot apply a completed chunk twice. There is no persistent queue or background
retry loop. Successfully completed earlier chunks are retained if a later chunk
fails. New arrivals are handled on the next request, not acknowledged by a time
cutoff. A request selects at most 20,000 observations.

Serialized context is capped at 1 MiB and chunks shrink to fit without truncating
observation content. Consolidation considers at most 500 memories selected by lexical overlap,
pinning and evidence recency; `context_limited` reports when the set is a subset.
It never truncates an individual observation. If one full observation plus the
selected context still exceeds the budget, the operation fails visibly. Exact
retrieval and consolidation support at most 10,000 active memories per user and
fail explicitly beyond that limit.

All model-facing instructions live in `prompts/system.md` and
`prompts/consolidation.md`. Typed JSON carries complete observations and current
memories, including IDs, timestamps, user/sender references, and source IDs.
Prompt versioning is derived from the contents of both embedded Markdown files.

New and reinforced memories preserve observation provenance. Contradictions create
a replacement and retain the old memory as history; contradicted and expired
memories are excluded from both current recall and profiles. Evidence confidence does not
automatically decay with age. Relevance does decay, with a 90-day base half-life
and reinforcement-based preservation. With an embedder configured, recall
fuses lexical and exact cosine ranks. There is no fuzzy auto-merge
that might erase distinct facts; the model explicitly reinforces existing IDs.

Observation idempotency keys are unique **within each user**. Generated IDs include
the user while import/API idempotency keys remain compatible with legacy imports,
so repeated imports of the same payload remain no-ops. Migration 003 preserves
all prior migration files and historical data, retires only legacy in-flight batch
rows as failed, and never acknowledges their observations.

## Checks

```bash
go test ./...
go vet ./...
go build ./...
# Optional: a dedicated disposable test database, never DATABASE_URL.
CORTEX_TEST_DATABASE_URL='postgres://user:pass@localhost/cortex_test?sslmode=disable' go test ./...
```

Database regression tests are skipped unless `CORTEX_TEST_DATABASE_URL` is
explicitly set. They create an isolated schema and remove only that schema on
completion. Use a dedicated test database with schema/extension permissions.
Tests use a fake inference boundary; they do not call a live model.

See [the synthetic walkthrough](examples/README.md) for sample observations and
[the design note](docs/design.md) for the storage and inference boundaries.

## License

MIT. See [LICENSE](LICENSE) and [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

## Semantic retrieval and lifecycle

Embeddings are optional. Configure `CORTEX_EMBEDDING_URL`,
`CORTEX_EMBEDDING_MODEL`, and optionally `CORTEX_EMBEDDING_KEY` to use a standard
OpenAI-compatible `/embeddings` service. The chat gateway and embedding service
can be different providers. No model weights or vector extension are bundled.
Embeddings are stored as PostgreSQL arrays and searched by exact cosine distance
within the bounded active set. An approximate index is not required.

The Go API accepts `WithEmbedder(embedder)` and
`WithRetentionPolicy(policy)` options on `NewEngine`. An embedder implements
`Model() string` and `Embed(context.Context, []string) ([][]float64, error)`.
Use a model identifier that includes the weights and preprocessing version.

Successful consolidation attempts a bounded embedding refresh. Its `index`
result reports incomplete indexing without rolling back committed facts.
`POST /v1/index?user=...` retries missing embeddings. Recall returns its mode,
indexed count and incomplete flag. A provider error does not silently downgrade
to lexical search; request `mode=lexical` explicitly when a fallback is wanted.

Additional operations:

- `POST /v1/maintain?user=...`: archive up to 1,000 expired or low-activation memories.
- `PUT /v1/memories/{id}/retention?user=...`: set `pinned`, `expires_at`, and `restore`.
- `DELETE /v1/memories/{id}?user=...`: forget a memory and its connected source lineage.
- `POST /v1/feedback`: record `id`, `user_id`, `memory_id`, `retrieval_id`, `used`, and optional boolean `success`.
- `GET /v1/evidence?user=...&memory=...`: inspect up to 1,000 supporting observations.

The corresponding Go methods are `Maintain`, `SetRetention`, `Forget`,
`Feedback`, `Evidence`, `Index`, and `RecallWithOptions`. An identical feedback
event ID can be retried; a changed event with the same user-scoped ID is rejected.
Only a used memory with a known outcome changes utility. Reads and unused results
do not train it. This is an inspectable running estimate, not a learned model.

Decay changes relevance, not evidence confidence. The default policy archives
unpinned memories below 0.05 activation once they are at least 30 days old, when
`Maintain` is called. Set a zero half-life to disable age decay. Explicit expiry
applies to pinned memories too. Archive preserves evidence and can be restored.

Forgetting is permanent. It removes connected contradiction versions and any
memories sharing source observations, so forgetting one fact from a multi-fact
observation can remove several facts. It deletes those source observations and
related embeddings and feedback. User-scoped hashes suppress identical content
or idempotency keys on reimport. Reworded new evidence is not semantic-matched
against a deleted secret. Previously sent provider prompts cannot be recalled.

Migration 004 preserves existing facts and derives evidence dates from their
sources where available. **Upgrade all writers before enabling forgetting or
maintenance.** Older binaries can write data without these new policies.
