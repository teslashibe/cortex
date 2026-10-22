# Classifier balancing development experiment

Class balancing recovered some minority memory types in this experiment, but
durable retention remained poor and acceptance coverage fell. The default stays
unweighted. All predictions remain advisory; none changed stored memories.

## What was compared

We refitted the two linear classification heads on each existing encoder: the
frozen MiniLM encoder and the encoder previously adapted with SetFit. Each encoder
was held fixed while comparing unweighted loss with inverse-frequency class
weights. The encoder files were verified against their existing manifests.
Unweighted refits reproduced the original probabilities exactly in this local run.

The data and project splits were unchanged: 3,032 provisional teacher-labeled
candidates and 648 synthetic candidates, with 2,061 training, 1,148 development
and 471 test rows in total. The natural test partition contains 363 candidates,
of which 310 have type labels. No human-reviewed labels were added. Synthetic
examples are excluded from the results below.

Both heads use C=10, seed 17 and at most 1,500 iterations. Class weights depend
only on each head's training labels. Each head's temperature is chosen by minimum
negative log likelihood on the original mixed development partition, from
0.5, 0.75, 1, 1.5, 2, 3 and 5. Acceptance retains the existing 0.8 thresholds.
Both loss settings were specified before running; no setting was promoted.

The test partition was already examined in earlier work and motivated this
experiment. Its results are development evidence, not independent confirmation.
The [aggregate results](classifier-balance-2026-10-22.json) include the protocol,
development and test metrics, class supports, and baselines fixed from training.
Conversations, individual predictions, embeddings and trained weights remain local.

## Natural test results

| Encoder and head loss | Retention accuracy | Retention macro-F1 | Durable found | Type macro-F1 | Joint accepted | Wrong among accepted |
| --- | --- | --- | --- | --- | --- | --- |
| Frozen, unweighted | 59.2% | 0.339 | 5/37 | 0.158 | 61/363 | 18/61 |
| Frozen, balanced | 50.4% | 0.338 | 5/37 | 0.216 | 9/363 | 3/9 |
| Adapted, unweighted | 64.2% | 0.341 | 1/37 | 0.160 | 45/363 | 13/45 |
| Adapted, balanced | 55.6% | 0.354 | 2/37 | 0.224 | 13/363 | 2/13 |

Macro-F1 averages all four retention classes or all six type classes, with zero
for undefined F1. Behavior and relationship have no natural test support and
remain unevaluated. Aggregate accuracy is dominated by temporary and project
labels: the training-majority baselines reach 69.7% retention and 92.3% type
accuracy on this partition. All four configurations fall below those accuracy
baselines.

| Supported memory type | Test support | Adapted, unweighted correct | Adapted, balanced correct |
| --- | --- | --- | --- |
| Fact | 9 | 0 | 2 |
| Preference | 14 | 0 | 4 |
| Project | 286 | 285 | 258 |
| Routine | 1 | 0 | 0 |

The balanced adapted model recovers some facts and preferences while losing
project predictions. Its joint acceptance coverage falls from 12.4% to 3.6%.
Two errors among 13 accepted predictions cannot establish a reliable error rate.
The durable class still loses 35 of 37 examples.

On the natural development partition, adapted-head retention macro-F1 changes
from 0.417 to 0.444 and type macro-F1 from 0.169 to 0.183. Accepted disagreements
increase from 9/71 to 4/15. The improvement in macro-F1 therefore does not establish
a better operational acceptance policy even on development data.

## Tooling changes

`--class-weight balanced` is an optional training setting for either backend.
It changes the classification heads; SetFit's encoder adaptation remains the same.
The manifest records the setting. Reports now show each class's support,
precision, recall and F1, label provenance, the training-majority baseline, and
the complete joint abstention policy. Missing classes have null recall instead
of a measured zero. Calibration assigns each prediction to exactly one bin.

The 11 classifier contract tests pass. A full CLI training run reproduced the
balanced frozen-encoder heads from this comparison. Eight local HTTP inference
cases matched offline probabilities within 0.000001. Changing every test label
left the fitted heads and their temperatures unchanged. Accuracy, macro-F1 and
confusion-matrix calculations also matched scikit-learn on randomized fixtures.

## Next experiment

Create an independently reviewed label set with explicit retention rules and
enough examples of each type to measure recall. Keep a separate sample with the
natural class distribution for coverage and error estimates. Group complete
projects before splitting, reserve new projects for confirmation, and resolve
annotation disagreements before fitting. Another model may propose labels, but
that is not human review.

Compare unweighted and balanced heads on the same reviewed data. Develop the
retention objective directly, since the current encoder adaptation trains on
type labels. Select models and acceptance thresholds on development data, then
freeze them before opening the new test partition. Report per-class failures,
accepted error, coverage and the training-majority baseline together. Evaluate
any proposed memory-writing policy against downstream retrieval and correction
tests before enabling it.

The separate source-reader priority is to improve unconditional answers and
measure consolidated-memory-only retrieval against raw-source and combined
retrieval under the same token and call budgets. This classifier experiment
provides no new evidence of reader quality or superiority over other platforms.
