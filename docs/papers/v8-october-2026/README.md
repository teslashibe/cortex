# Academic manuscript, revision 8

Cortex: Sleep-Inspired Memory Consolidation for Language Agents

This edition incorporates the broader source-authorship audit and reports
22 pairs of classifier heads fitted after conservative source quarantine.
The corrected dataset contains 5,548 training candidates and 424 development
candidates with provisional model labels. Unweighted retention macro-F1 reaches
0.393, but the model recovers none of 14 durable examples. Class balancing
recovers five while making 65 additional durable predictions that disagree with
the provisional labels. No classifier is promoted.

The paper also reports twelve synthetic-anchor adaptation runs, thirty fictional
memory episodes, an assertion-boundary contrast study, hybrid retrieval diagnostics,
and eight fictional historical-ingestion episodes. It separates source-admission
errors from consolidation errors and qualifies earlier employment-instruction
flags as policy-dependent. These studies have no independent human judgments and
do not establish natural-conversation safety or a sleep-inspired learning advantage.

Ten propositions and two theorems establish conditional software properties.
The added information-limit result explains why a reader given identical inputs
cannot distinguish histories whose withheld corrections require different answers.
These proofs do not establish semantic truth, biological fidelity, learning
convergence, or formal verification of the implementation.

The public artifacts contain aggregate results only:

- `expanded_classifier_results.json`: all 22 fits, class supports and confusion
  matrices, development calibration, training-majority references, and curve ranges
- `development_results.json`: synthetic-anchor, fictional history, assertion,
  retrieval, and answer diagnostics, with resource counts and qualifications
- `historical_order_results.json`: chronological and backfill diagnostics,
  including the preserved setup failure before provider work
- `learning_results.json`, `learning_diagnostics.json`, `serving_validation.json`:
  earlier replay diagnostics and model/API validation
- `results.json` and `classifier_analysis.json`: qualified historical measurements
- `manifest.json`: implementation identity, protocol hashes, and artifact hashes

Private conversations, email, annotations, per-example outputs, provider responses,
experiment scripts, and trained weights remain outside Git. Email has not entered
these fits. The 421 unexamined LongMemEval-S cases and reserved natural confirmation
records remain untouched. Source-subset range bars are not confidence intervals.

Independent human review, better class coverage, representation experiments,
and fresh matched-budget confirmation remain necessary. The benchmark protocol
specifies the comparison required before making production-quality claims.
Paper revisions are independent of software releases. The published software
release is v1.5.0, an interim milestone that includes the implementation used
in these local diagnostics. Earlier paper editions are unchanged.

Build with `xelatex main.tex`, `bibtex main`, and further `xelatex main.tex`
passes until references stabilize. The checked PDF is `paper.pdf`.
