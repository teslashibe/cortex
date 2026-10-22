# Qualification of the replay development cohorts

The expanded source audit conducted after paper revision 7 links 35 of its 42
primary development candidates to connected source groups represented in
training. Those cohorts do not establish independent-conversation transfer.
The recorded scores remain available as regression diagnostics.

| Historical cohort | Candidates | Linked to training components | Source not mapped by this check |
| --- | ---: | ---: | ---: |
| Older primary cohort | 24 | 21 | 1 |
| Newer primary cohort | 18 | 14 | 3 |

The check matches complete source messages of at least 100 normalized characters
to the expanded source graph. That graph combines recorded lineage, session and
repository metadata, and detected copied passages. The four unmapped candidates
remain unresolved. Three other mapped candidates have no detected training link;
this does not certify their independence. A source-component link need not mean
that the exact candidate was copied into training.

Revision 7's term “component-disjoint” describes the grouping available when
that experiment was frozen. The broader graph exposes additional relationships.
The PDF and its artifact hashes are preserved so the original account remains
auditable; this notice qualifies its empirical interpretation.

## Attribution correction

A subsequent inspection of training-label disagreements found scheduled
automation envelopes in records carrying the `user` role. The earlier filter
missed those envelopes. A structural source check also identifies mixed records
containing pasted content, injected instructions, command output or other
transport context. Their contents may include genuine user requests, but each
delivery cannot be counted as an independent human-authored observation.

| Replay partition | Candidates | Automated or mixed source | Scheduled automation subset |
| --- | ---: | ---: | ---: |
| Older training | 317 | 138 | 130 |
| Newer training | 168 | 58 | 49 |
| Older primary development | 24 | 20 | 18 |
| Newer primary development | 18 | 14 | 14 |

Thus 34 of the 42 primary development candidates have an additional authorship
qualification. This check uses complete original source records, with every
source in these four partitions resolved. It does not change their recorded
labels or scores. Removing evaluation rows alone would leave affected training
in place and would not establish a corrected experiment.

Before fitting the expanded corpus, the labeling run was stopped and its records
preserved. The corrected pipeline quarantines 1,362 of 6,425 selected source
records, covering 15,385 of 24,497 extracted candidates. It also excludes 35
otherwise eligible candidates whose previous annotation attempts were issued
but not validated. This leaves 9,077 candidates for provisional review. Valid
completed judgments are reused; missing judgments follow a separately frozen
protocol. No expanded-corpus model had been fitted before this correction.

The correction was made during development after inspecting partial labels.
It is not an untouched preregistration. Whole-source quarantine is conservative
and can remove genuine text embedded in mixed records. Remaining authorship is
still unverified, and the existing source-component boundaries and reserved
confirmation assignments have not been relaxed.

The source audit does not change the conditional software proofs or the recorded
API test outcomes. It further limits the classifier quality evidence. No trained
classifier has been promoted, and no learning advantage or biological REM
simulation has been established. The existing conclusion that generated replay
failed the joint development criteria on its recorded data remains unchanged;
those data do not support a conclusion about transfer to human conversations.

Further model selection uses the expanded grouping, with separate conversations
reserved for later confirmation. Independent human labels, downstream factual
retention and unsupported-memory judgments remain necessary.

The [aggregate audit](expanded-source-audit-2026-10-22.json) contains cohort counts
and provenance hashes only. Source messages, identifiers, mappings and labels
remain private.
