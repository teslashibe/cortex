# Academic manuscript, revision 6

Cortex: Sleep-Inspired Memory Consolidation for Language Agents

This edition makes the sleep analogy explicit in the title and abstract. It
separates implemented consolidation from proposed NREM-like replay and REM-like
recombination, with no claim to simulate biological sleep.

It retains the measured v1.4.0 results and adds an analysis of the frozen
classifier confusion matrices. Constant-label checks, per-class support and
recall, macro-F1, Brier scores, and calibration qualify the headline accuracy.
No new model evaluation was run for this editorial and analytical revision.

Vendor-reported Mem0, Zep, and Hindsight scores are separately sourced and are
not ranked against Cortex's smaller experiment. The final substantive section
specifies the protocol needed for controlled platform comparisons, classifier
promotion, and replay experiments. Proposed tests are not completed results.

Only aggregate metrics and public sources are included. Private transcripts,
annotations, per-example evaluations, and trained weights remain outside Git.
The prior paper and v1.4.0 release artifacts remain unchanged.

Build with `xelatex main.tex`, `bibtex main`, and two further `xelatex main.tex`
passes. The checked PDF is `paper.pdf`.
