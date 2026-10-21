# Cortex design

Cortex stores observations and uses a model to propose durable memories. The
database validates and commits those proposals; recall reads current memories.
The application using Cortex owns identity, authorization, source consent, and
the choice of model endpoint.

## Observations and memories

Observations preserve content, timestamps, source references, and attribution
metadata. Import idempotency is scoped to a user. A memory records its kind,
summary, confidence estimate, reinforcement history, and supporting observation
IDs. Confidence is model-supplied and is not a calibrated probability.

The five memory kinds are behavior, preference, relationship, project, and
routine. Supported model operations insert a memory, reinforce an existing
memory with new evidence, or replace a contradicted memory. Replaced memories
remain as history and are excluded from current recall and profiles.

## Consolidation

A database session lock serializes consolidation for each user. A request takes
a snapshot of up to 20,000 pending observations and processes sequential chunks
of at most 200. Each chunk sees the current memories, including earlier committed
chunks. The complete serialized model request must fit within 1 MiB. Chunks
shrink to fit; individual observations are not truncated.

No database transaction stays open during model inference. The model response
must be a strict operations envelope. Invalid operations, unknown fields,
duplicate JSON keys, invalid references, or evidence outside the selected chunk
reject the whole chunk. All memory changes, exact observation acknowledgements,
and the completed consolidation record share one transaction.

An interrupted chunk leaves observations pending. A later request may repeat
model work, but cannot apply a completed chunk twice. Earlier successful chunks
remain committed if a later chunk fails. A model call has a two-minute timeout;
the enclosing consolidation has a thirty-minute timeout.

The session lock requires a direct PostgreSQL connection or session pooling.
Transaction-pooling proxies are not supported.

## Recall and limits

Recall uses PostgreSQL text candidates and a heuristic combining confidence,
text overlap, recency, and reinforcement count. It does not use embeddings.
Reads do not automatically create feedback or change confidence. Signals are
explicit writes.

All current memories are considered when building consolidation context. A large
memory set can exceed the request limit and stop further consolidation. Cortex
does not silently truncate that set or provide automatic compaction.

The package has no authentication service, autonomous scheduler, learned memory
policy, or source-permission revocation mechanism. Raw observations remain stored
after consolidation. A lower memory-row count is not a measure of storage
compression or downstream answer quality.

See [the security boundary](../SECURITY.md) before exposing Cortex through an
application, and [the synthetic walkthrough](../examples/README.md) for a small
manual example.
