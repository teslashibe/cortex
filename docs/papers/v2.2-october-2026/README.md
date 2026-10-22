# October 2026 paper, revision 2.2

[Read the PDF](paper.pdf) or edit [the LaTeX source](main.tex).

This revision describes [Cortex v1.1.0](https://github.com/teslashibe/cortex/releases/tag/v1.1.0),
code commit `73c41b2f40516345bbec2bde2429aaf69d68699a`.
It reports a synthetic control, selected local Codex and Claude session tests,
and a small public-data smoke test. These development diagnostics are not a full
benchmark or a population-level accuracy estimate.

The Humanizer embedded editing pass was applied to the prose. The source and
PDF are versioned together; [the manifest](manifest.json) records their hashes.
Older paper revisions remain unchanged.

Build with `xelatex main.tex` twice, or open this directory in the Codex LaTeX editor.
The evaluation corpus, actual session messages, extracted memories, prompts,
model responses and database exports remain outside Git. This directory contains
only the paper, source, version metadata and aggregate results within the paper.

See the [grading erratum](ERRATUM.md) before interpreting the public-case table.
