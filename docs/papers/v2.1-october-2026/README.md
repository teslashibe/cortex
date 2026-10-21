# October 2026 paper, revision 2.1

[Read the PDF](paper.pdf) or edit [the LaTeX source](main.tex).

This revision describes [Cortex v1.0.1](https://github.com/teslashibe/cortex/releases/tag/v1.0.1),
code commit `b8bf2da1e06c2e2e73f8b8fbff08fa843327f551`.
Paper revisions and software versions are separate.

## Changes from revision 2

- Applied the Humanizer skill's embedded editorial pass on 21 October 2026, preserving the technical claims, equations, citations and reported results
- Added the exact code commit and paper revision to the document and PDF metadata
- Specified the next evaluation sequence, candidate changes and release checks, explicitly marked as pending work
- Preserved [revision 2](../v2-october-2026/paper.pdf) and the unchanged [March paper](../v1-march-2026/paper.pdf)

The LongMemEval, LightMem reproduction and Memora paper records were checked through Scarlett on 21 October 2026. This revision contains no new experimental results. The local diagnostic is still a
development evaluation, not an independent benchmark or comparison with other products.

Build with `xelatex main.tex` twice, or open this directory in Codex's LaTeX editor.
Only aggregate results and methodology belong in this repository. Credentials,
local sessions, benchmark corpora, prompts, raw responses and database exports remain outside Git.

The [manifest](manifest.json) records the software commit and source/PDF hashes.
