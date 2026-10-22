# Security

Cortex is an embedded library and a trusted-localhost service. Its HTTP routes
do not authenticate callers or authorize their requested user scope. Applications
that embed or proxy Cortex must enforce those decisions before calling it.

The profile tier filter is a display heuristic. It is not a guarantee that a
profile is safe to share. Review generated memories and authorize access in the
application that presents them.

Consolidation sends observations and existing memories to the configured model
endpoint. Choose that endpoint according to your data-handling requirements.
`IndexObservations` and `IndexObservationWindows` send raw text to the embedding
endpoint only when explicitly called. `ClassifyCandidates` sends selected source
spans and surrounding context to a configured classifier only on an explicit call.
Its output is advisory and cannot change memory state. Original-observation recall retains source text and attribution;
access to this route must be authorized separately by the host when appropriate.
Cortex does not provide encryption at rest or automatic source-permission
revocation. Restrict database access and protect backups separately.

`ConsolidateSelected` requires the host to supply an exact input selection and
a permission guard. The guard must check all selected observations and every
source of the selected memories. Its commit-phase policy locks must remain
held by the supplied transaction until the memory write commits. Use qualified
table names when the host policy tables live in another schema. The guard
cannot coordinate a separate database or an external authorization service
atomically. Memory reads and recall still require the host's current access
checks; an earlier successful consolidation does not grant future access.

When an optional `WindowSegmenter` is configured, window indexing and window
recall send eligible raw sources to that endpoint, including lexical-only window
recall. Use a trusted local segmenter or authorize the provider for those sources.
Token-aware segmentation and embedding must use the same tokenizer and special
token policy. The embedding endpoint must reject oversized inputs independently.
The optional Python adapters bind to loopback and have no authentication.

Keep credentials, exports, database dumps, and personal profiles out of source
control. Keep classification journals and weights outside Git as well. Deleting
a Cortex source cannot remove it from an external dataset or from model weights.
The repository's examples contain synthetic data only.

## Reporting a vulnerability

Use GitHub private vulnerability reporting when it is enabled for this repository.
If it is unavailable, open an issue requesting a private contact route without
including credentials, personal data, or exploit details.

Include the affected revision, configuration, expected behavior, and a minimal
reproduction with synthetic data in the private report.
