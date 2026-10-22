# Evidence reader

`Engine.AnswerEvidence` and `POST /v1/evidence/answer` provide an optional reader
for applications that want an answer with a source ledger. Calling either sends
selected raw observation excerpts to the configured model. The host must authorize
that transfer and enforce the caller's owner scope. The HTTP service trusts its
local caller, as the other Cortex endpoints do.

```json
{
  "user_id": "example-owner",
  "question": "How many days elapsed between my arrival and departure?",
  "as_of": "2026-10-21T12:00:00Z",
  "timezone": "America/Los_Angeles",
  "lexical_only": false
}
```

The operation makes three model calls, plus at most one format-repair call: query planning, evidence extraction,
and composition. It makes no automatic transport retries and writes no observations, memories,
embeddings, feedback, or training labels. A malformed successful response or an inexactly copied quotation can be repaired once; all source checks still apply. Each call has the existing model timeout;
the operation has a four-call deadline. Empty retrieval returns an insufficient
answer after planning. Provider failures return an error.

The reader combines up to four query variants with an additional lexical channel.
An invalid query plan falls back to the original question and reports
`query_plan_fallback`; quotation and owner checks are unchanged.
Simple plural normalization, month-name aliases, speaker preference, overlapping
window suppression, and a two-excerpt-per-source cap improve evidence diversity.
The evidence array contains at most 80 excerpts and 32,000 UTF-8 bytes, including
JSON metadata. This bound is not a token count. An `index_incomplete` flag identifies
incomplete semantic indexing; lexical retrieval remains available.

Every returned fact includes an exact quotation from a supplied excerpt, source ID,
Unicode code-point offsets, source hash and observation time. The accompanying
claim is a model interpretation. A valid quotation does not prove that a claim is
true, current, or attributable to the user. The final answer cites fact IDs. A cited date assumption or inferred year makes the response status `conditional`; applications also receive the assumptions separately.

Date phrases must occur verbatim inside a quotation before they can anchor a
calculation. An unmatched proposed phrase is discarded with the rule
`rejected_nonverbatim_expression`; it cannot support arithmetic. Missing-year
month/day forms may use the observation year under the explicit rule
`assumed_observation_year`. Applications requiring no year inference should use
`ResolveEventDates`, whose stricter contract remains unchanged.

`days` computes the difference between civil dates, excluding the start date.
`months` counts completed calendar months: January 31 to February 28 is zero.
Negative intervals are symmetric. `count` counts distinct fact IDs chosen by the
model as events. Event identity, completion, and period membership remain model
judgments; counting source rows is not an event inventory. The response exposes
calculation inputs and the computed value separately from generated prose.

All selected sources are checked again after composition. A changed, forgotten,
archived or expired source causes an error without returning partial evidence.
This is a final-snapshot guarantee. It cannot retract evidence already sent to a
provider or an answer returned before a later deletion.

Use the ledger to audit answers. Do not use a model's `answered` status as proof
of exhaustive retrieval, semantic correctness, or permission to mutate memory.
