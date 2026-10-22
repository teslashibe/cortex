# Academic manuscript, revision 4

Cortex: Provenance-Preserving Memory with Selective Classification for Language Agents

The manuscript extends the formal state model with exact token budgets,
source-linked date resolution, and a project-separated training experiment.
Its conditional proofs concern the stated abstract machine. They do not prove
semantic accuracy or formally verify the implementation.

The manifest identifies the implementation and final artifact hashes. Only
aggregate measurements are published. Local conversations, source-span labels,
per-example predictions, provider responses and weights remain private.
Earlier paper revisions remain unchanged in their versioned folders.

Build with `xelatex main.tex`, `bibtex main`, and two further `xelatex main.tex`
passes. The release PDF is `paper.pdf`.
