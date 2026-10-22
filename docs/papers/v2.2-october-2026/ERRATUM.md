# Grading erratum

A case-level inspection on 21 October 2026 found two errors in the automatic
judging of the seven-case public development probe:

- The memory-only preference answer was `UNKNOWN`. It did not meet the
  personalized recommendation requirement and should fail.
- The memory-only degree answer supplied the correct subject, Business
  Administration, with a qualification about graduation. It should pass the
  subject-answer criterion used in the audit.

These changes offset each other: the memory-only aggregate remains 3/7, while
source and combined results remain 5/7. The original table's case-level labels
for User fact and Preference are reversed. The original PDF is preserved as a
historical artifact.

The audit was performed by Codex against the stored questions, reference answers,
and reader outputs. It is not an independent human adjudication. The revised
[academic manuscript](../v3-october-2026/main.tex) reports this limitation and
separates automatic checks from reviewed case decisions.
