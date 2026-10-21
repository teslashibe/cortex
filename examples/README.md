# Synthetic walkthrough

All people, statements, and conversation identifiers in this directory are
synthetic. The example demonstrates a manual workflow; model wording and
operation choices can vary.

Start Cortex with a local PostgreSQL database and a configured model endpoint,
following the root README. From the repository root, save two observations:

```sh
curl --fail-with-body http://127.0.0.1:8090/v1/observations \
  -H 'Content-Type: application/json' \
  --data-binary @examples/observations.json
```

Run consolidation and inspect the resulting memories:

```sh
curl --fail-with-body -X POST \
  'http://127.0.0.1:8090/v1/consolidate?user=demo-user'
curl --fail-with-body \
  'http://127.0.0.1:8090/v1/profile/demo-user?tier=internal' \
  -H 'Accept: application/json'
```

Consolidation calls your configured model and may incur its normal usage cost.
Inspect memory source IDs and the consolidation result before treating model
output as an accepted fact.

Save an explicit correction, consolidate again, and recall the updated topic:

```sh
curl --fail-with-body http://127.0.0.1:8090/v1/observations \
  -H 'Content-Type: application/json' \
  --data-binary @examples/correction.json
curl --fail-with-body -X POST \
  'http://127.0.0.1:8090/v1/consolidate?user=demo-user'
curl --fail-with-body \
  'http://127.0.0.1:8090/v1/recall?user=demo-user&q=coffee&limit=10'
```

A correct contradiction proposal replaces the old tea memory while preserving
its history. The database checks structure, ownership, and evidence references;
the model remains responsible for interpreting the statements correctly.

Repeating the same observation upload is idempotent. Successful consolidation
acknowledges the exact processed observations, so another consolidation with no
new observations returns `noop`.
