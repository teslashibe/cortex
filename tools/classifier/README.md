# Private classification experiments

These tools classify exact candidate spans into retention and semantic type. The
Go API runs in **shadow mode**: predictions never change stored memories. The
classifier is separate from summary extraction and conflict resolution.

Class balancing and the expanded reports described here are development tooling
on `main`; they are not included in the v1.4.0 release tag.

Use a dedicated Python environment. Install `requirements.txt` there. Training and
serving run locally; only downloading the pinned public encoder needs network
access. Labeling explicitly sends the selected observations to your chosen model
endpoint. Do not supply sources you have not approved for that provider.

## Measured status

The [revision 7 paper](../../docs/papers/v7-october-2026/paper.pdf) preserves a
historical 363-candidate natural test partition with provisional teacher labels. The
adapted model reached 64.2% retention agreement and 91.9% type agreement, below
constant-label accuracy checks of 69.7% and 92.3%. The type partition contains
286 projects among 310 typed candidates. Joint acceptance covered 12.4% of
candidates and disagreed on 28.9% of accepted predictions. Retention macro-F1
was 0.341 and type macro-F1 was 0.160 across all declared classes.

A subsequent source audit found machine or mixed authorship in nine of those
test candidates and related-project overlap across the original partitions.
Affected training must also be corrected; deleting a few test rows cannot
establish independent transfer. These historical scores are diagnostic only.

The corrected private replay experiment trains the encoder and heads jointly
and compares ordinary training, repeated examples, paraphrases, and recombinations
over three seeds. It reserves connected session/repository components, matches
paired labels and source schedules, and fixes updates and padded dimensions.
The main development cohorts are small: 24 older and 18 newer candidates.
Generated examples never become observations. This experiment does not measure
factual recall or unsupported-memory creation, and no trained model is promoted.

These results do not justify autonomous memory decisions. Include constant-label
baselines, per-class support, and independent human review in new experiments;
headline accuracy alone hides minority-class failures. The paper's benchmark
protocol is proposed work, not a completed comparison with other platforms.

An [October 22 development experiment](../../docs/experiments/classifier-balance-2026-10-22.md)
tested class balancing on the same previously examined data. Balancing the adapted
encoder's heads recovered 2/9 facts and 4/14 preferences, but only 2/37 durable
candidates. Joint acceptance fell to 13/363, with two disagreements. These small,
provisional results do not justify changing the default or enabling memory writes.

## Data contract

Each JSONL row needs `id`, `group`, `split`, `text`, `context`, `retention`, `kind`,
and `label_source`. Splits are `train`, `dev`, and `test`. Retention is `durable`,
`temporary`, `ignore`, or `uncertain`; kind is one of the six Cortex types, or null
for an ignored/unresolved candidate. Keep text to 512 Unicode code points and
context to 1024. Offsets refer to the immutable original source, never a paraphrase.

A `group` must identify the whole conversation or related project. Assign groups
before fitting. Join sessions that cross related repositories into the same
component; a working-directory name alone is insufficient. Exclude automatic
context and mixed speaker wrappers before labeling. Recover a human answer from
a wrapper only with explicit attribution and new labels. Keep near-duplicate
conversations and every generated derivative in the same partition. The
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
python tools/classifier/train.py train \
  --data "$PRIVATE_EXPERIMENT/labels.jsonl" \
  --output "$PRIVATE_EXPERIMENT/balanced" --backend linear --class-weight balanced
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
80 contrastive steps by default, then fits the same linear heads. Optional
`--class-weight balanced` weights each head's training examples by inverse class
frequency: `training_count / (class_count * class_support)`. It applies to the
linear heads, not SetFit's encoder adaptation. The default is `none`; the manifest
records the choice. Development loss selects temperature from 0.5, 0.75, 1, 1.5,
2, 3, 5. Neither weights nor temperature are selected using test labels.

Reports include per-class support, precision, recall and F1, plus balanced
accuracy, Brier score, calibration error, and coverage/error at 0.8. `macro_f1`
averages all declared classes, assigning zero to undefined F1; `supported_macro_f1`
averages only classes present in that evaluation partition. Missing classes have
null recall and appear in `unevaluated_classes`. Every confidence contributes to
exactly one calibration bin, including values on bin boundaries.

`majority_baseline` fixes each head's constant prediction from the training
partition, then evaluates it on the other partitions. `by_label_source` reports
human, teacher and synthetic rows separately, retaining that same baseline.
`joint_policy` mirrors Go's abstention policy, including the type threshold for
retained candidates and unconditional abstention on `uncertain`. A head's
`coverage_at_0.8` alone does not measure this joint policy. No accepted predictions
means the error estimate is null. Synthetic scores do not certify deployment
performance. Both model variants remain marked `shadow_only`.
The manifest hashes the encoder files, heads, input data, and label provenance.

Training and serving count the complete formatted input with the encoder's own
tokenizer, including special tokens, and reject inputs beyond its declared limit.
They never silently truncate. Split an oversized candidate upstream or review a
smaller source span. The new `candidate-context-v2` format replaces one exact
candidate occurrence in the surrounding context with `[candidate]`, keeping its
surrounding text and avoiding duplication. The manifest records this format and
token limit. Older artifacts retain their original format and receive the same
strict limit check when served by this tool.

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
Malformed annotations remain in the journal as `pending_invalid`, with explicit
errors; training refuses them until reviewed. The tool never fills in a missing
type by guessing.

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
