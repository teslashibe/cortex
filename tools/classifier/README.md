# Private classification experiments

These tools classify exact candidate spans into retention and semantic type. The
Go API runs in **shadow mode**: predictions never change stored memories. The
classifier is separate from summary extraction and conflict resolution.

Use a dedicated Python environment. Install `requirements.txt` there. Training and
serving run locally; only downloading the pinned public encoder needs network
access. Labeling explicitly sends the selected observations to your chosen model
endpoint. Do not supply sources you have not approved for that provider.

## Data contract

Each JSONL row needs `id`, `group`, `split`, `text`, `context`, `retention`, `kind`,
and `label_source`. Splits are `train`, `dev`, and `test`. Retention is `durable`,
`temporary`, `ignore`, or `uncertain`; kind is one of the six Cortex types, or null
for an ignored/unresolved candidate. Keep text to 512 Unicode code points and
context to 1024. Offsets refer to the immutable original source, never a paraphrase.

A `group` must identify the whole conversation or related project. Assign groups
before fitting. Keep near-duplicate conversations in the same partition. The
loader checks exact normalized candidate text and group overlap; it cannot detect
all semantic duplicates. Fit thresholds and temperature on development data.

`label_source` is `human`, `synthetic`, or `teacher`. Model-proposed annotations
remain `teacher` until a person reviews them. Record the actual reviewer and
revision in your private journal. `--allow-provisional` permits an explicitly
provisional experiment; it does not turn model labels into ground truth.

## Train and compare

Data and weights must be outside every Git checkout. The tool rejects checkout
paths, including resolved symlinks. Create a private experiment directory first.

```sh
python tools/classifier/train.py train \
  --data "$PRIVATE_EXPERIMENT/labels.jsonl" \
  --output "$PRIVATE_EXPERIMENT/linear" --backend linear
python tools/classifier/train.py train \
  --data "$PRIVATE_EXPERIMENT/labels.jsonl" \
  --output "$PRIVATE_EXPERIMENT/setfit" --backend setfit
```

For a reproducible fabricated control (648 examples), run:

```sh
python tools/classifier/synthetic.py --output "$PRIVATE_EXPERIMENT/synthetic"
```

This creates the labeled JSONL and its protocol outside Git. The wrapper-family
split shares topic vocabulary across partitions and is not a natural-language
transfer benchmark. Actual session labels need independent review.

The default encoder revision is pinned. Linear heads use normalized embeddings,
C=10, seed 17 and at most 1500 optimization iterations. The SetFit comparison uses
80 contrastive steps by default, then fits the same linear heads. Development
loss selects temperature from 0.5, 0.75, 1, 1.5, 2, 3, 5. Reports include F1,
Brier score, calibration error, and coverage/error at 0.8. Synthetic scores do not
certify deployment performance. Both model variants remain marked `shadow_only`.
The manifest hashes the encoder files, heads, input data, and label provenance.

## Review journal

```sh
python tools/classifier/label.py \
  --observations "$PRIVATE_EXPERIMENT/observations.json" \
  --output "$PRIVATE_EXPERIMENT/review.jsonl"
```

Input is an array containing `id`, `user_id`, `group`, `content`, and source
metadata for each approved observation. The default teacher endpoint is localhost
port 8088, model `gpt-6-luna-medium`. Configure `--base-url` and `--model` explicitly
for another provider; credentials come from `LLM_API_KEY`. The tool does not retry
provider failures or guess offsets for repeated text. Its output is a review
queue, not a finished training split. Select and correct candidates, record the
review, and allocate complete groups before using the training tool.

## Local inference

```sh
python tools/classifier/train.py serve --model "$PRIVATE_EXPERIMENT/linear" --port 8092
```

Set `CORTEX_CLASSIFIER_URL=http://127.0.0.1:8092/classify` and set
`CORTEX_CLASSIFIER_MODEL` to the model identifier in the manifest. Optional remote
credentials use `CORTEX_CLASSIFIER_KEY`. The Python service binds to localhost,
verifies artifact hashes, does not log request text, and accepts one request at a
time. It is an experimental adapter, not an authenticated public service.

`POST /v1/classify` accepts `user_id` and a `candidates` array. Each candidate has
`id`, `observation_id`, `start_rune`, and `end_rune`. The Go API fetches only eligible
sources in that owner scope and rechecks them after prediction. It returns
probabilities, selected labels, abstention, policy version, and source hashes.
All output is advisory. It never creates a memory or a training event.

Actual source records, review journals, checkpoints, and per-example evaluations
must stay private. Deleting an observation in Cortex does not erase a separately
exported dataset or a model that was trained from it.
