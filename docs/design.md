# Cortex design

Cortex stores observations, consolidates durable memories, and retrieves them
for an application. The host owns authentication, consent, scheduling, and
provider credentials. The package owns validation and memory state transitions.

## Evidence and state

The six kinds are fact, behavior, preference, relationship, project, and routine.
Summaries are bounded to 512 Unicode characters; the complete request remains
bounded to 1 MiB. A fact holds an explicit statement that does not imply a preference or repeated
behavior, such as a device, qualification, or dated location.
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
Retries can repeat provider work but cannot apply a completed chunk twice. A
successful model response that fails parsing or field validation gets at most one
format-repair call using the same source window. Both responses count toward token
usage. Transport errors are not retried. If the repair fails or the combined
request exceeds the wire budget, the chunk remains pending.

A call snapshots up to 20,000 observations and processes chunks of at most 200.
The model sees at most 500 current memories, selected by lexical overlap with
the chunk, pinning and evidence recency. `context_limited` exposes selection;
it does not imply that every possible contradiction was examined. The complete
wire request must fit within 1 MiB. Chunks shrink without truncating observations.
An oversized individual observation fails visibly and remains pending.
`unreferenced_observation_ids` lists completed observations that supported no
operation. This is an audit aid: irrelevant or ambiguous observations can
correctly appear there, and a cited observation can still contain an omitted fact.

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
in the same user scope.

`RecallObservations` separately searches up to 20,000 original observations per
user with lexical overlap and optional semantic rank fusion. It returns original
content, timestamps and attribution without asserting that the contents are
current facts. The host decides when to use this path and how to combine results
within its reader budget. `IndexObservations` is an explicit opt-in: ordinary
memory indexing never sends raw conversations to the embedding provider.

Sources of any superseded, archived or expired memory are excluded, including
sources shared with an otherwise current fact. This deliberately sacrifices
some coverage to preserve lifecycle decisions. An unassociated observation can
still contain outdated or quoted information; the reader must check its date
and speaker. Source indexing and recall recheck eligibility after provider work.
`ForgetObservation` removes a source, exact-content copies and derived memories
through the same import barrier and tombstone mechanism as `Forget`. It works
when no memory was retained. A missing observation is an idempotent empty result.

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

Migrations 004 and 005 are additive. Migration 005 stores optional observation
embeddings with cascading deletion and adds a source lookup index. Upgrade all
writers before creating `fact` memories; old validators do not accept the new
kind. Lifecycle operations also require every writer to enforce the forgetting
tombstones and archive state introduced in migration 004. Session pooling or a direct connection is required; transaction
pooling is incompatible with consolidation's session advisory lock.

The HTTP service trusts local callers. Profile filtering is a display heuristic,
not authorization. No autonomous scheduler, source connector, model-training
job, source-permission reconciliation, or background worker is hidden inside
the package. See [SECURITY.md](../SECURITY.md) before exposing an API.

## Consolidation references

The model sees short references local to its current request. Cortex resolves them
to exact stored observation and memory IDs before applying its usual scope and
transaction checks. Aliases cannot collide with real IDs, and duplicate sources
after resolution are rejected. Existing memory source lists stay in storage and
are omitted from inference; they are not new supporting evidence.
