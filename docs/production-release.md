# Production release contract

Cortex v1.4.0 is an additive release for a host-owned memory service. It keeps
the existing database schema and consolidation behavior. The host supplies
authentication, owner authorization, scheduling, backup policy, and permission
to send source text to each configured model provider.

## Upgrade

1. Install the tagged package or verified command archive.
2. Keep the existing database and provider configuration. No new migration is
   required beyond the schema already used by v1.2.0.
3. If enabling tokenizer-aware windows, configure the segmenter and embedder
   from the same model/tokenizer contract. Reindex explicitly. Changed identities
   produce separate vector namespaces; old vectors are never treated as new ones.
4. Confirm indexing completes, query a known source, and check deletion removes
   its evidence. Treat `index_incomplete`, `truncated`, and `changed_during_read`
   as limitations of the returned evidence.

The optional Python adapters run on loopback and install separately. The local
embedding adapter hashes its tokenizer and ONNX model; inference rejects an
input that exceeds the exact token budget. The Go client validates coverage and
declared counts, while the provider remains responsible for counting honestly.

## Classification status

Classification is advisory in this release. It cannot write memories, suppress
observations, or authorize forgetting. The private experiments use provisional
model labels and have no human-reviewed test labels. They do not justify an
automatic retention policy.

A later mutation policy needs an independently reviewed corpus, frozen project
partitions and acceptance rules, and measured risk for the accepted predictions.
The paper states the assumptions of its selective-risk bound. As one explicit
candidate criterion, an upper bound of 5% at 95% confidence requires at least
600 accepted independent examples when observed error is zero under that bound.
Correlated project examples, label mistakes, or tuning on the test set invalidate
that calculation. Review coverage and performance by retention class as well.

## Evidence and dates

Multi-query retrieval returns evidence, not a complete event inventory. The host
can identify distinct events itself or use the optional evidence reader. The reader selects event identities with a model, so its counts still require that interpretation to be correct. Date resolution preserves exact source spans and an explicit timezone;
it does not prove that a described event occurred. Unsupported expressions remain
unresolved. Calendar-day subtraction is separate from elapsed-hour calculations.

The [optional evidence reader](evidence-reader.md) adds exact quotation validation,
source eligibility checks after composition, and explicit arithmetic inputs. It
makes three model calls, plus at most one format-repair call and never changes memory state. Conditional date
interpretations are returned as assumptions. Classification and retrieval quality
remain empirical properties, separate from persistence guarantees.

## Release checks

Required checks are PostgreSQL integration tests with the race detector, Python
input-contract tests, `go vet`, builds, vulnerability checks, and a complete Git
history secret scan. Public archives include checksums and dependency notices.
The accompanying paper separates exploratory quality measurements from tested
persistence and input-validation contracts. Private conversations, labels,
per-example outputs, model weights, and database snapshots are excluded.
