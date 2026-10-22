# Cortex

A small Go/PostgreSQL memory engine for language agents: `Observe`, `Recall`,
and `Consolidate`. It keeps original observations, derives memories with source
references, and retrieves current information across sessions.

Inference uses the provider-neutral `llm` package and a non-streaming
OpenAI-compatible `/chat/completions` endpoint. The default target is
[Open Agent API](https://github.com/teslashibe/open-agent-api) with
`gpt-6-luna-medium`.

## Current status

The current software release is [v1.4.0](https://github.com/teslashibe/cortex/releases/tag/v1.4.0).
The latest paper is revision 6,
[Cortex: Sleep-Inspired Memory Consolidation for Language Agents](docs/papers/v6-october-2026/paper.pdf).
Software and paper version numbers are independent. The v1.4.0 release includes
paper revision 6 and preserves revision 5. Both papers and their sources are
available in this repository.

- Implemented: source attribution, transactional consolidation, contradiction
  history, lexical and optional semantic retrieval, relevance decay, expiry,
  explicit forgetting, and outcome feedback.
- Experimental: source-assisted answer generation and locally trained retention
  and memory-type classifiers. Classifier predictions never change memory state.
- Supplied by the host: authentication, owner authorization, scheduling,
  provider permissions, and backup policy.

The [evaluation results](#evaluation-and-benchmarks) separate persistence checks
from answer quality. This release has not demonstrated parity with other memory
platforms or reliable autonomous classification.

## Sleep-inspired design

Complementary learning systems and sleep research motivate a separation between
rapid capture and slower integration. `Observe` records attributed source text;
`Consolidate` processes pending observations into reusable memories; `Recall`
retrieves eligible memories with their provenance.

Consolidation runs when the application calls it. Cortex does not simulate sleep
stages or implement generative dreaming. Source-grounded replay and recombined
synthetic training examples are proposed experiments in the paper. Relevance
decay is an explicit retrieval policy, separate from evidence confidence.

## Install

Use the tagged Go package:

```sh
go get github.com/teslashibe/cortex@v1.4.0
```

Install the commands with Go:

```sh
go install github.com/teslashibe/cortex/cmd/cortex@v1.4.0
go install github.com/teslashibe/cortex/cmd/cortex-import@v1.4.0
go install github.com/teslashibe/cortex/cmd/cortex-once@v1.4.0
```

Prebuilt command archives and checksums are available from the
[v1.4.0 release](https://github.com/teslashibe/cortex/releases/tag/v1.4.0).
For the source examples below, clone this repository and run the commands from
its root directory.

## Run

Requires Go 1.26.10 or newer and PostgreSQL with permission to install `pg_trgm`.
Release validation uses PostgreSQL 17. The server applies embedded migrations on
startup; use a database owned by the embedding application.

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

### Configuration

Core settings for `cortex` and `cortex-once`:

| Variable | Default or requirement |
| --- | --- |
| `DATABASE_URL` | Required PostgreSQL connection string |
| `LLM_BASE_URL` | `http://127.0.0.1:8088/v1` |
| `LLM_API_KEY` | Optional bearer token; depends on the provider |
| `CORTEX_MODEL` | `gpt-6-luna-medium` |
| `CORTEX_LISTEN` | `127.0.0.1:8090`; HTTP server only |

Optional integrations configured by the HTTP server:

| Variables | Behavior |
| --- | --- |
| `CORTEX_EMBEDDING_MODEL` | Enables embeddings; no embedding model is selected by default |
| `CORTEX_EMBEDDING_URL`, `CORTEX_EMBEDDING_KEY` | Separate provider base URL and optional bearer token; the URL defaults to `http://127.0.0.1:8088/v1` |
| `CORTEX_SEGMENTER_URL`, `CORTEX_SEGMENTER_ID`, `CORTEX_SEGMENTER_MAX_TOKENS` | Full segmentation endpoint, identity, and positive token limit; all required when enabling a segmenter |
| `CORTEX_SEGMENTER_KEY` | Optional segmenter bearer token |
| `CORTEX_CLASSIFIER_URL`, `CORTEX_CLASSIFIER_MODEL` | Full classification endpoint and model identity; both required when enabling classification |
| `CORTEX_CLASSIFIER_KEY` | Optional classifier bearer token |

Embedding URLs are base URLs: the client appends `/embeddings`. Segmenter and
classifier URLs include their endpoint paths, such as `/v1/segment` and
`/classify`. The Go API exposes the same integrations through `WithEmbedder`,
`WithWindowSegmenter`, and `WithClassifier`. `cortex-once` configures only the
consolidation model; the HTTP server owns the optional environment integrations.

The application schedules consolidation and maintenance. Model calls made by
the commands have a two-minute timeout, and a complete consolidation request has
a 30-minute timeout. The adapter sends `faithful: false` and `prewarm: false`,
which are Open Agent API extensions; verify support with another chat provider.
The default Open Agent API model alias selects medium reasoning. Cortex has no
separate reasoning-effort setting or provider account pool.

Transport failures are not automatically retried. One additional model call per
consolidation chunk may repair a successful response that violates its JSON or
summary-length contract. `format_repairs` reports these attempts; a failed
repair leaves the chunk pending.

## Import and consolidate

`cortex-once` optionally imports a Claude export, synchronously consolidates
pending observations, and prints a profile. The export format is independent
of the inference provider. Both local commands support the export structure
read by `LoadClaudeExport`; the API accepts observations from other sources.

```bash
go run ./cmd/cortex-once --dir /path/to/claude-export --user demo-user --tier internal
# Consolidate already-imported observations without another import:
go run ./cmd/cortex-once --user demo-user --chunk 100 --by-day
```

`--chunk` is a maximum observation count (1-200, default 200). `--by-day` keeps UTC
days separate while preserving chronological processing. Each chunk sees the
memories committed by earlier chunks. Rerun the same command to process whatever
remains pending.
The synchronous command has no `--resume` or batch-polling mode.
`cortex-import` remains available for importing through the HTTP API. Both import
commands default to the `default` user scope; pass `--user` explicitly when
keeping separate histories.

For an API walkthrough with fabricated observations and an explicit correction,
see [the synthetic example](examples/README.md). Example data is separate from
private conversation tests.

## HTTP

The listener is intended for a trusted host. Use `user` in query parameters for
query-scoped operations, `user_id` in JSON bodies for body-scoped operations,
and the path parameter for profiles. These identifiers do not authenticate a
caller. GET search endpoints accept `q` and `limit`; `mode=lexical` explicitly selects
lexical retrieval where supported. Profiles default to `tier=public`; request
`tier=internal` explicitly for the full display. Tier selection is not an
authorization boundary.

| Method and path | Operation |
| --- | --- |
| `GET /healthz` | Process health and consolidation prompt version |
| `POST /v1/observations` | Store `{user_id, observations: [...]}` |
| `POST /v1/consolidate` | Consolidate the selected user's pending observations |
| `POST /v1/dream` | Compatibility alias for synchronous consolidation |
| `GET /v1/recall` | Ranked current memories |
| `GET /v1/profile/{user}` | Markdown profile; JSON with `Accept: application/json` |
| `POST /v1/index` | Refresh up to 1,000 memory embeddings |
| `GET /v1/observations/recall` | Retrieve original observations |
| `POST /v1/observations/index` | Index up to 1,000 original observations |
| `GET /v1/observations/windows` | Retrieve source excerpts |
| `POST /v1/observations/windows/index` | Index source windows for up to 1,000 observations |
| `POST /v1/evidence/recall` | Retrieve across one to four explicit queries |
| `POST /v1/evidence/dates` | Resolve source-bound date candidates in an explicit timezone |
| `POST /v1/evidence/answer` | Generate an experimental answer with source quotations |
| `GET /v1/evidence` | Inspect up to 1,000 sources for the requested `memory` |
| `POST /v1/classify` | Return advisory classifications of exact source spans |
| `POST /v1/maintain` | Archive up to 1,000 expired or low-activation memories |
| `PUT /v1/memories/{id}/retention` | Set `pinned`, `expires_at`, or `restore` |
| `DELETE /v1/memories/{id}` | Forget a memory and its connected source lineage |
| `DELETE /v1/observations/{id}` | Forget an observation and derived memories |
| `POST /v1/signals` | Store an explicit user-scoped signal |
| `POST /v1/feedback` | Record idempotent, attributed memory-use feedback |

An empty consolidation backlog returns `status: "noop"`. Concurrent
consolidation for the same user returns HTTP 409. A failure includes completed
runs and the failed run in `result`. `unreferenced_observation_ids` lists
processed observations that supported no operation; it does not establish that
consolidation missed a fact. `/v1/dream` has the same synchronous behavior and
provides no separate sleep process.

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

Consolidation instructions live in `prompts/system.md`,
`prompts/consolidation.md`, and `prompts/repair.md`. Their combined content hash
versions the consolidation prompt. The optional evidence reader has separate
prompts in `evidence_answer.go`.

Typed JSON preserves complete observation content, timestamps, and attribution.
Request-local aliases reduce identifier-copying errors. Cortex resolves aliases
to exact stored IDs before validating and committing operations. Historical
memory source lists and observation idempotency keys are omitted from model
context; provenance remains in storage.

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

Feedback accepts `id`, `user_id`, `memory_id`, `retrieval_id`, `used`, and an
optional boolean `success`.

The lifecycle and indexing Go methods are `Maintain`, `SetRetention`, `Forget`,
`Feedback`, `Evidence`, `Index`, and `RecallWithOptions`. An identical feedback
event ID can be retried; a changed event with the same user-scoped ID is rejected.
Only a used memory with a known outcome changes utility. Reads and unused results
do not train it. This is an inspectable running estimate, not a learned model.

Decay changes relevance, not evidence confidence. The default policy archives
unpinned memories below 0.05 activation once they are at least 30 days old, when
`Maintain` is called. Use `WithRetentionPolicy` with a zero half-life to disable
age decay. Explicit expiry applies to pinned memories too. Archive preserves evidence and can be restored.

Forgetting is permanent. It removes connected contradiction versions and any
memories sharing source observations, so forgetting one fact from a multi-fact
observation can remove several facts. It deletes those source observations and
related embeddings and feedback. User-scoped hashes suppress identical content
or idempotency keys on reimport. Reworded new evidence is not semantic-matched
against a deleted secret. Previously sent provider prompts cannot be recalled.

Migration 004 preserves existing facts and derives evidence dates from their
sources where available. **Upgrade all writers before enabling forgetting or
maintenance.** Older binaries can write data without these new policies.

## Retrieve original evidence when needed

`Recall` returns validated memories. `RecallObservations` returns original
observations with their speaker metadata and timestamps. Call the second method
explicitly when an answer needs evidence that consolidation may have omitted.
Raw evidence can contain quotations, assistant suggestions or outdated details;
it must not be treated as a validated fact or as instructions.

```go
// Send raw text to the configured embedder only after the host authorizes it.
index, err := engine.IndexObservations(ctx, userID, 1000)
_ = index // If Incomplete, call again to index the next bounded batch.
if err != nil { return err }
evidence, err := engine.RecallObservations(ctx, userID, question,
    cortex.RecallOptions{Limit: 5})
_ = evidence // Enforce a reader budget and preserve source attribution.
```

Without an embedder, observation search is lexical. Set `LexicalOnly: true` to
request this explicitly when a configured provider is unavailable. The exact
scan rejects sets above 20,000 eligible observations. Sources supporting a
superseded, archived or expired memory are excluded, even if shared with another
fact. Use `ForgetObservation` to remove an unretained source; deleting it also
removes exact-content copies, derived memories and associated vectors.

## Retrieve bounded source excerpts

Use source windows when a whole observation would consume too much reader context:

```go
_, err := engine.IndexObservationWindows(ctx, userID, 1000)
if err != nil { return err }
windows, err := engine.RecallObservationWindows(ctx, userID, question,
    cortex.RecallOptions{Limit: 8})
_ = windows
```

Each hit has exact text, parent observation ID, half-open Unicode code-point
positions, speaker references, and `observed_at`. This timestamp is the message
time; event dates need evidence from its text. Windows contain at most 512 code
points with 128 overlap by default. These are character bounds, not token bounds: choose an
encoder that accepts them without truncation, or configure `WithWindowSegmenter`.
A user's full eligible window set
is capped at 100,000. Token-aware coverage is checked as segmentation results
arrive; an invalid or over-capacity result prevents vector publication.

`POST /v1/observations/windows/index?user=` explicitly indexes this representation.
`GET /v1/observations/windows?user=&q=&limit=` returns excerpts; `mode=lexical`
selects lexical-only retrieval. Existing whole-observation routes are unchanged.
Window indexing reports complete observations indexed, while recall reports
indexed windows. Index publication is atomic per observation and deletion cascades
to every window. The same lifecycle and owner checks apply to both source paths.

The optional `HTTPSegmenter` uses an exact tokenizer service. Set
`CORTEX_SEGMENTER_URL`, `CORTEX_SEGMENTER_ID`, and `CORTEX_SEGMENTER_MAX_TOKENS`
(plus `CORTEX_SEGMENTER_KEY` if needed). Cortex validates full source coverage,
original offsets, progress, and declared token counts. The embedding service must
independently reject oversized inputs; Cortex cannot verify a remote tokenizer's
claim. Vector namespaces include segmentation identity, so changing the tokenizer
or policy requires reindexing. An optional [local ONNX adapter](tools/embeddings/README.md)
performs both segmentation and strict embedding checks with one tokenizer.
Configured segmentation sends sources to that endpoint during window recall too.

## Retrieve events and resolve dates

`RecallEvidence` merges one to four explicit query variants, deduplicates windows,
and rechecks source eligibility. `POST /v1/evidence/recall` accepts
`{"user_id":"example","queries":["museum visit","gallery trip"],"limit":40}`.
Results include `truncated`, `index_incomplete`, and `changed_during_read`. The
`sources` count is a count of retrieved observations, not distinct real events.
The host owns query generation, event identity, corrections, and evidence budgets.
Even an untruncated result reports `coverage: "retrieved_evidence_only"`.

`ResolveEventDates` and `POST /v1/evidence/dates` accept an explicit IANA timezone
and exact source-span candidates, using the same candidate shape as classification.
Supported expressions are ISO or English dates with a year, `today`, `yesterday`,
`N days ago`, and `last <weekday>` (strictly before the observation's local date).
Other expressions return `resolved: false`. Results preserve the text, source
hash, rule and observation timestamp. Resolving a quoted or hypothetical date
does not establish that an event occurred. `CalendarDaysBetween` subtracts civil
dates, avoiding daylight-saving errors. No event records are written automatically.

## Evidence answers

`AnswerEvidence` and `POST /v1/evidence/answer` are optional, experimental reader
operations. They retrieve source windows, extract exact quotations, and compose
an answer with cited fact IDs and any date assumptions. Calling the reader sends
selected raw excerpts to the configured language model; the host must authorize
that transfer.

After storing observations, submit a question with an explicit timestamp
and IANA timezone:

```sh
curl --fail-with-body http://127.0.0.1:8090/v1/evidence/answer \
  -H 'Content-Type: application/json' \
  --data-binary '{
    "user_id": "demo-user",
    "question": "What did I say about coffee?",
    "as_of": "2026-10-21T12:00:00-07:00",
    "timezone": "America/Los_Angeles",
    "lexical_only": true
  }'
```

Use `POST /v1/observations/windows/index?user=demo-user` to index windows with an
embedder. Without embeddings, lexical window retrieval is available; indexing
is not required for that mode. The reader does not index or consolidate for you.

The normal path uses three model calls, with at most one additional repair.
Empty retrieval can return earlier. It supplies at most 80 excerpts and 32,000
serialized UTF-8 evidence bytes, including metadata. This is a byte limit, not
a token limit. The operation never writes observations, memories, embeddings,
feedback, or training labels.

The result includes `facts`, `citations`, `assumptions`, `calculation`,
`coverage`, `truncated`, `index_incomplete`, `model_calls`, and `format_repairs`.
Source eligibility is checked again after composition; an invalidated source
causes an error without partial evidence. Exact quotation checks establish
source support, while the interpretation remains model-generated.

Date differences use civil days or completed calendar months. Event counts rely
on the model's event selections. A response can be `conditional`; neither an
`answered` status nor untruncated retrieval proves that every relevant event
was found. See the [reader contract](docs/evidence-reader.md) for date rules,
query-plan fallback, and failure behavior.

## Experimental classifiers

The local training tools fit two classification heads on a pretrained
`all-MiniLM-L6-v2` sentence encoder:

- Retention: `durable`, `temporary`, `ignore`, or `uncertain`.
- Type: `fact`, `behavior`, `preference`, `relationship`, `project`, or `routine`.

A frozen-encoder baseline trains the heads alone; a SetFit variant adapts the
encoder before fitting the same heads. These are task-specific trained
parameters built on a third-party encoder. Cortex does not train the external
language model used for consolidation, and trained weights are not bundled.

The optional `Classifier` interface accepts exact candidate spans. Configure it
with `WithClassifier`, or set `CORTEX_CLASSIFIER_URL` and
`CORTEX_CLASSIFIER_MODEL` for the HTTP adapter. `POST /v1/classify` accepts:

```json
{"user_id":"example","candidates":[{"id":"candidate-1","observation_id":"source-id","start_rune":0,"end_rune":24}]}
```

The source must exist in that user's eligible observations. Spans are at most
512 code points; requests contain at most 128 candidates. Results separate
retention (`durable`, `temporary`, `ignore`, `uncertain`) from the six memory kinds.
They include all class probabilities, model and policy versions, and source
hashes. Probabilities do not replace evidence confidence.

The joint policy abstains below 0.8 retention confidence, for an `uncertain`
retention label, or below 0.8 type confidence on a retained candidate. These are
experimental thresholds, not established error guarantees.

Classification is **shadow-only**. It never changes memories or stores training
examples. Source eligibility is checked again after inference. The host owns
extraction, annotation review, private journals, and any later adoption decision.
See [the classifier workflow](tools/classifier/README.md) for local baseline and
SetFit training, data partitioning, calibration, and a provisional labeling tool.
Actual sessions, labels, model artifacts, and per-example evaluations belong
outside Git. A deleted source must also be removed from any host-managed exports;
source deletion does not erase information from separately trained weights.

## Evaluation and benchmarks

The release was checked with PostgreSQL integration tests and Go's race
detector, Python contract tests, builds, static analysis, vulnerability checks,
and secret scanning. These checks cover the tested persistence and input
contracts; they do not establish semantic completeness or general memory quality.

The [revision 6 paper](docs/papers/v6-october-2026/paper.pdf) reports the frozen
quality experiments and their limitations. The private corpus contains 2,814
explicit user messages from 119 sessions and 35 project groups. Natural
classification labels were proposed by a model and have not been independently
reviewed by humans.

### Source-assisted answers

| Sixty-case LongMemEval sample | Direct baseline | Source-checked reader |
| --- | --- | --- |
| Complete, unconditional answers | 44/60 | 36/60 |
| Conditional answers | 1/60 | 11/60 |
| Reference alignment, including conditional answers | 45/60 (75.0%) | 47/60 (78.3%) |
| Normal model-call budget | 1 | 3, plus at most one repair |

Both conditions could retrieve original messages. These scores do not isolate
consolidated-memory quality. The two-case difference does not establish a
reliable advantage, and the comparison did not equalize model-call cost. On the
separate twelve-case development set, one run reached 12/12, including four
conditional answers; the final repeat reached 11/12. Perfect repeatability has
not been demonstrated.

### Experimental classifiers

The adapted model was evaluated on 363 natural candidates from held-out project
groups; 310 had type labels.

| Measure | Adapted model | Constant-label accuracy check |
| --- | --- | --- |
| Retention agreement | 233/363 (64.2%) | Always temporary: 253/363 (69.7%) |
| Type agreement | 285/310 (91.9%) | Always project: 286/310 (92.3%) |
| Joint acceptance coverage | 45/363 (12.4%) | Not evaluated |
| Errors among accepted predictions | 13/45 (28.9%) | Not evaluated |

Type labels are heavily imbalanced: 286/310 are projects, and the adapted model
predicts project for 309/310 inputs. It recovered only 1/37 durable examples.
Retention macro-F1 was 0.341; type macro-F1 was 0.160 across all declared classes,
including two without test support. The constant-label rows are retrospective
accuracy checks, not validated policies. These results do not support autonomous
retention or a claim of general six-type classification.

### Comparative benchmark plan

No matched comparison with Mem0, Zep, or Hindsight has been run. Their published
scores use different systems and protocols and are reported separately in the
paper. A credible comparison needs:

1. A frozen full 500-question LongMemEval-S run, reporting the 79 previously
   examined cases separately from the remaining 421.
2. The same reader and evidence-token budget for retrieval comparisons, with
   complete-product comparisons reported as a separate track.
3. Raw-source, consolidated-memory-only, and combined retrieval conditions;
   repeated answers, case-level failure analysis, cost, and latency measurements.
4. Independently reviewed classifier labels, class-specific metrics, and fixed
   acceptance criteria before enabling learned memory mutations.

Source-grounded and synthetic replay are further controlled experiments. They
are not implemented dreaming behavior or completed benchmark results. The
paper's final protocol specifies these experiments and the evidence needed for
comparative claims.

## Papers

The current manuscript is
[Cortex: Sleep-Inspired Memory Consolidation for Language Agents](docs/papers/v6-october-2026/paper.pdf),
paper revision 6. It includes the biological motivation, conditional mathematical
proofs, experimental classifier analysis, platform context, and the comparative
benchmark protocol.

- [Versioned LaTeX source](docs/papers/v6-october-2026/main.tex)
- [Artifact manifest and hashes](docs/papers/v6-october-2026/manifest.json)
- [Aggregate measurements](docs/papers/v6-october-2026/results.json)
- [Paper history, including the original March paper](docs/papers/README.md)

The proofs concern the stated abstract state machine, not formal verification
of the implementation or proof that model-generated memories are true. Only
aggregate experimental results are public. Actual conversations, annotations,
per-example outputs, model weights, and database exports remain private.

## Checks

From the repository root:

```bash
go test -race ./...
go vet ./...
go build ./...
python3 -m unittest discover -s tools/classifier -p 'test_*.py'
python3 -m unittest discover -s tools/embeddings -p 'test_*.py'
```

Database regression tests are skipped unless `CORTEX_TEST_DATABASE_URL` is set.
Use a dedicated disposable PostgreSQL database with schema and extension
permissions, never the application's `DATABASE_URL`:

```bash
CORTEX_TEST_DATABASE_URL='postgres://user:pass@localhost/cortex_test?sslmode=disable' \
  go test -race -count=1 ./...
```

Tests create an isolated schema and remove that schema on completion. Unit and
database tests use a fake inference boundary; they do not call a live model.
Live model quality is evaluated separately as described above.

[GitHub checks](.github/workflows/ci.yml) also run vulnerability detection and a
complete Git-history secret scan. See the [release contract](docs/production-release.md)
for upgrade requirements and the [design note](docs/design.md) for ownership
boundaries.

## License

MIT. See [LICENSE](LICENSE) and [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
