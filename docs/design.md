# Cortex design

Cortex stores observations, consolidates durable memories, and retrieves them
for an application. The host owns authentication, consent, scheduling, and
provider credentials. The package owns validation and memory state transitions.

## Evidence and state

The five kinds are behavior, preference, relationship, project, and routine.
Insert, reinforce, and contradict operations require exact observation IDs.
Replaced memories remain as history. Evidence confidence is a model estimate,
not a calibrated probability. Processing time and last supporting observation
time are separate. Replaying older evidence does not refresh the evidence date.

A memory can be current, superseded, expired, archived, or permanently forgotten.
Only current memories participate in profiles, consolidation, and recall.
Archive preserves evidence. Explicit forgetting removes connected versions and
shared sources, including related vectors and feedback. User-scoped hashes
reject exact content and import-key replays. A shared import barrier serializes
forgetting with concurrent imports. Reworded new observations are not compared
semantically with deleted content.

## Consolidation and indexing

A PostgreSQL session advisory lock serializes each user's consolidation. No
transaction remains open during model inference. Strict JSON validation rejects
invalid fields, duplicate keys, out-of-window sources and invalid memory IDs.
All operations, exact acknowledgements, and the completed run share one commit.
Retries can repeat provider work but cannot apply a completed chunk twice.

A call snapshots up to 20,000 observations and processes chunks of at most 200.
The model sees at most 500 current memories, selected by lexical overlap with
the chunk, pinning and evidence recency. `context_limited` exposes selection;
it does not imply that every possible contradiction was examined. The complete
wire request must fit within 1 MiB. Chunks shrink without truncating observations.
An oversized individual observation fails visibly and remains pending.

Requested and returned model identifiers are stored separately. A model call
has a two-minute timeout; consolidation has a thirty-minute timeout.

An optional embedder refreshes missing vectors after consolidation releases its
lock. Embeddings are published only if the memory still exists, is current and
has the same content. Model identity, content hash and vector accompany each
record. Incomplete indexing is reported and can be retried with `Index`.

## Retrieval

Recall uses lexical token overlap and optional exact cosine similarity. Each
channel contributes reciprocal rank `1 / (60 + rank)`. Confidence, activation
and utility add a small adjustment of `0.00001` times their sum. This keeps
relevance primary: age must not bury an exact match under unrelated facts.
An English stopword list removes
common function words; other words and short identifiers remain. These are
inspectable heuristics, not calibrated relevance probabilities.

Vectors are stored as PostgreSQL arrays. Exact retrieval and consolidation
support at most 10,000 active memories per user and fail beyond that bound.
No vector extension or additional database is required. Approximate indexes
are a future scale decision. `RecallWithOptions` reports lexical/hybrid mode,
candidate count, indexed count and incomplete coverage. A failed query embed
does not silently fall back. `LexicalOnly` requests that mode explicitly.

Recall rechecks current rows after an embedding request, so a memory forgotten
while that request was running is removed from the result. Reads never mutate
confidence or fabricate training labels. `Evidence` resolves exact source IDs
in the same user scope; it is not an independent raw-document search service.

## Decay and feedback

Activation is `2^(-age / (halfLife * (1 + ln(reinforcedCount))))`, using elapsed
evidence age. Pinned memories have activation one. Confidence is unchanged.
The default half-life is 90 days. Explicit `Maintain` calls archive memories
below 0.05 activation after at least 30 days, or at their expiry date. Expiry
takes precedence over pinning. These defaults have not been optimized on a
public benchmark; applications can provide another policy.

`Feedback` requires a user-scoped event ID, retrieval ID, memory ID, whether it
was used, and an optional known success outcome. Identical retries do nothing;
conflicting retries fail. Only used memories with known outcomes update utility.
The estimate starts with a symmetric two-observation prior. A positive outcome
adds one success; a negative adds zero. Utility modestly adjusts ranking and
does not alter evidence confidence. Legacy `WriteSignal` remains an event log.

## Operational boundary

Migration 004 is additive, but every writer must be upgraded before using the
new lifecycle operations. Older code does not enforce forgetting tombstones or
archive state. Session pooling or a direct connection is required; transaction
pooling is incompatible with consolidation's session advisory lock.

The HTTP service trusts local callers. Profile filtering is a display heuristic,
not authorization. No autonomous scheduler, source connector, model-training
job, source-permission reconciliation, or background worker is hidden inside
the package. See [SECURITY.md](../SECURITY.md) before exposing an API.
