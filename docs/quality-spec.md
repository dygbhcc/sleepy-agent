# Quality spec v3.2 (draft): what a bad script is

Owner: the channel owner. Taste is a human decision, not the model's.
Status: draft. Nothing here changes the QA gate yet (project rule 11).
v3 is a proposal until the owner has confirmed section 1 and section 1b.
Changes from earlier versions are in the log at the end.

In a product, this file would be built per user (for example from a short survey
when a channel is set up). Language defects would stay global; preferences would
be per user. Here the owner is the only user.

## 0. What the owner looks at (the four questions)

A perfect script does not exist, so good does not mean flawless. The owner reads
a script and asks four questions:

1. **Disturbing**: is there anything in it that disturbs, that does not help
   someone relax (threat, insects, blades, sudden events)?
2. **Repetition**: is there too much repetition?
3. **Coherence**: does it hold together in meaning?
4. **Concept**: does it fit the topic and the style?

A script is good when none of the four is a real problem. Everything below is
evidence for one of these questions. The defects are not extra rules.

| question | defects that give evidence |
|---|---|
| 1 Disturbing | H7, H11, part of H6 |
| 2 Repetition | H1, H3 |
| 3 Coherence | H2, H2b, H5a, H5b, H5c, H6 |
| 4 Concept | H4, H10 |

How much counts as "too much" is the owner's call, not a number. Where a
threshold below is marked provisional it can only warn, until labels agree with
it on scripts it was not set from.

### Candidate fifth question: listening flow (v3.2, not a check yet)

The product is listened to, not read. After listening to four strict-d scripts
with a browser voice, the owner said the sentences were too short and too
alike, and that the endings should fit together and not stop on a sharp word.
No check and no judge looked at this, and the labels were not made by listening.
Measured on 39 scripts (strict-c and strict-d): every script has 44 to 48
sentences of 10 to 14.6 words on average, and sentence length does not separate
good from bad. The cause is the generator prompt (8 to 20 words, exactly 4
sentences per paragraph), not the model.

Candidate signals, all unvalidated: mean sentence length, the spread of sentence
length, the number of "and" per sentence, the share of sentences that end on the
same word as another sentence, and whether the last word is short and hard. They
can be counted by code. Whether a count tracks what the owner hears is not
measured. Until a fresh batch is generated with a different prompt, listened to
and labelled before any checker output is read, this question reports nothing
and rejects nothing (rules 13 and 14).

## 1. The one question

> Would the owner publish this script after a small edit?

- **small edit**: touches at most 5 sentences or 10 percent of the sentences,
  whichever is larger. No paragraph is rewritten, reordered or removed.
- **good**: publishable as is, or after a small edit.
- **bad**: needs more than a small edit. A rewrite, not a fix.

Every defect has its own reject threshold, so one fixable slip does not reject a
script. A combined rule closes the opposite gap: many small slips of different
kinds can add up to more than a small edit (section 6, rule 3).

Paragraph: a block separated by a blank line. A script with no blank lines is
read as groups of 4 sentences.

## 1b. How the owner labels (proposal)

Direct good or bad was too coarse: the line moved between scripts. From the next
batch the owner gives, per script:

- Four scores from 1 to 5, one per question in section 0.
  - 5: no problem, nothing noticed.
  - 3: noticed, fixable with a small edit.
  - 1: would be rewritten.
- A decision: `publish`, `small-edit` or `reject`.
- Tags: which questions caused it (`q1` to `q4`) and the defect words the owner
  used (for example `filler-tail`, `insects`).

Proposed rule of record: good is `publish` or `small-edit`. The scores are kept so
that checkers can be compared with them by rank (Spearman correlation) instead of
by a pass or fail line. Scripts the owner rescores after seeing checker output
are marked `relabel-after-checker` and counted apart. A few scripts are rescored
a week later to measure the owner's own consistency.

## 2. How the judge must behave

1. Every defect starts as `present: false`. It becomes true only with evidence.
2. Evidence is a verbatim quote copied from the script. Code checks that the
   quote is a substring of the script. If it is not, the finding is dropped as
   not found.
3. Each defect is judged on its own, in the order below, in one reply. No overall
   impression and no scores.
4. The judge never decides whether it likes the script. Taste lives in
   Preferences (section 5), which cannot reject.
5. When unsure, the answer depends on the mode (section 6a): while the owner
   reads every script, `present: false`; under autonomy, `unsure: true`, which
   sends the script to review.
6. For H5a, H5b, H6 and H10 the judge first lists the markers it found
   (paragraph number and quote), then decides. This stops it from judging from
   memory of the whole text.
7. The judge never rewrites or suggests fixes. It only reports.
8. Temperature 0. A second pass runs only for defects reported as present, and it
   must return the same quote. A defect is kept only if both passes agree.

## 3. Hard defects

"Reject when" is the threshold. Below it, the defect is a warning and the script
can still pass. Sentences flagged as warnings count toward the combined rule.

### Checked by code

| id | defect | how to count | reject when | not a defect |
|---|---|---|---|---|
| H1 | Filler tail: a sentence ends with an appended phrase that can be deleted without changing the meaning | (a) Word list: softly, tonight, today, now, here, in silence, in quietness, of peace, for souls, onward forward, ever gentler, for night. (b) Structural: the last 1 to 3 words repeat across sentences even if not on the list. The structural signal starts as a warning only, until it is measured | 60 percent or more of sentences (provisional, v3.1). Warn from 10 percent; the warning does not count toward the combined rule. The old 25 percent flagged 4 of 8 owner-good scripts in strict-c (shares 25, 31, 31, 38 percent) | One closing "softly" in an otherwise clean script; natural repeats such as "of the night" |
| H2 | Dangling sentence: ends on an article, preposition or conjunction (the, a, of, in, by, and, to, for, with) | Last word of each sentence | 6 or more sentences. Fewer is a small edit and a warning | A sentence ending on a noun or verb |
| H3 | Repetition, inside the script | (a) Exact duplicate sentence. (b) Same opening 4 words in 40 percent of paragraphs. (c) A 3 to 4 line cycle repeated 2 or more times. (d) Paragraph overlap: two paragraphs share 60 percent or more of their content words | (a) 6 or more duplicate sentences (fewer is a small edit). (b) at 40 percent. (c) once. (d) once | One motif returning in new surroundings each time |
| H4 | Subject missing: candidate only | The topic's key noun appears in the body fewer than 2 times. Code reports a candidate; the judge decides whether the subject is shown (H4 in the judge table). Code never rejects on this | Never by code | A subject told through its imagery: a library script with shelves, books and pages that rarely says "library" |
| H11 | Unsettling words (candidate, from strict-c/run-2: "knife, bugs etc not relaxing words") | Words that do not help someone relax, matched as whole words: insect, insects, beetle, bug, bugs, spider, sting, knife, blade used as a weapon. The exact list is not confirmed by the owner yet | Warn only until the owner confirms the list and a threshold. "blade" for a blade of grass is not a defect | Grass blades; a single "insects" inside a sleepy night scene is for the owner to decide |
| H8 | The script talks about itself or the episode | Existing `MetaSentences` check. Also catches narrator instructions leaking into the text, for example "Very quiet closing stillness settles" | Any | None |
| H9 | Length and banned words | Existing QA gate | Per gate | |

Title and topic: the title should contain a word from the topic. This is a
warning only. It is not a reason to reject, and the prompt does not force the
topic into the text.

### Checked by the judge (checklist, yes or no, with quote)

| id | defect | test the judge applies | reject when | not a defect |
|---|---|---|---|---|
| H2b | Dangling modifier: a sentence ends on a bare adjective or adverb that breaks the word order, often repeating a word from earlier in the sentence | Quote each such sentence | 6 or more sentences. Fewer is a small edit and a warning | Natural inversion in a clearly poetic line |
| H5a | Season or weather conflict | List every weather, season and plant or animal marker with its paragraph. Mark conflicts using the incompatible sets below | 1 conflict between two clearly stated markers, quote both | Metaphors that do not state weather |
| H5b | Time regression | List every time of day marker in order. The night must only deepen: dusk, night, deep night. A return to morning, dew, sunrise or dawn after night began is a regression | 1 regression, except a final single sentence that only hints at dawn | One night progressing from dusk to deep night |
| H5c | Place incoherence | Places that cannot coexist: a lighthouse on a coast with a birch forest, snowy field and meadow lake in the same scene | The main place contradicts 2 or more other stated places | Fantasy (Myth) moving between places while one motif carries through |
| H6 | Physical nonsense | Space settings: wind, breeze, scent, hum, sound or warmth stated as real in vacuum. Also contradictions such as "cold vacuum" next to "warm blanket" | 2 or more statements, or 1 statement of a clear contradiction | Poetic metaphor that does not claim a physical sensation |
| H7 | Threat or tension | Hunter and prey in the same script (fox and rabbit, owl and mouse), watching or stalking behavior, sudden events, danger words | 3 or more sentences. 1 or 2 is a small edit and a warning | Sleeping dragon, gentle giant, sleeping owl, a statue of an owl |
| H10 | Imported imagery | Imagery recycled from unrelated settings that the topic does not support. Example: a lighthouse script whose main content is birch, moss, snow, lake and rain | More than half the paragraphs contain no image tied to the topic | One or two shared sensory images (moonlight, mist) |

| H4 | Subject missing | Given a code candidate, does the body show the topic's subject with concrete imagery? Quote two sentences that show it, or answer present true | The topic's subject is not shown anywhere (strict-a/run-1: no ring gap, no moon, no named planet) | Subject carried by its imagery without the name |

### Incompatible sets for H5a

- Snow or ice with: dew, fireflies, green leaves in rain, wildflowers, warm dusk
  air, fish, frogs, lily pads, crickets.
- Fireflies, crickets, frogs or night-blooming jasmine (warm season) with: snow,
  frost, ice, bare branches.
- Sunrise or morning dew with moonlight, stars or fireflies once night has begun
  is H5b, not H5a.

## 4. Anchor examples

Anchors are given by file, because titles repeat across runs. Only scripts the
owner labelled and gave a reason for are used as few-shot examples. A script
without a stated reason is kept in the list but not used as an example.

| file | owner verdict | decisive reason |
|---|---|---|
| scriptstats-v3-b/run-4.md | good | clean language, one amber lantern motif across places, one night that progresses, closes by dimming one lantern |
| scriptstats-strict-a/run-8.md | good | reason not given by the owner. Not used as a few-shot example |
| scriptstats-strict-a/run-6.md | good | reason not given by the owner. Not used as a few-shot example |
| scriptstats-strict-a/run-5.md | good | one scene, dusk to night (the timeline can be tidied) |
| scriptstats-strict-a/run-4.md | good | owner: the concept is very good, some lines are broken. Read as a small edit: H2b ("stones gentle" twice, "hollows deep") and one duplicate sentence, 4 to 5 sentences |
| scriptstats-strict-a/run-2.md | good after small edit | hunter and prey sentences (fox watching, owl over a mouse) must go (H7, 2 to 3 sentences); no address to the listener; varied sentence endings |
| scriptstats-strict-a/run-1.md | bad | H1 (appended fillers: "together", "onward forward", "ever gentler", "for souls"), H4 (Saturn never named in the body), H6 (breeze and scent in space), no progression across 12 paragraphs |
| scriptstats-strict-a/run-7.md | bad | H1 at 100 percent ("in quietness" on every sentence); repair would touch every sentence |
| scriptstats-v2-a/run-2.md | bad | owner: the parts do not hold together. Read as H5a: dew, rain, snow and fireflies in one night; the title says Dawn |
| scriptstats-strict-a/run-3.md | bad | reason not given by the owner (H1 at 46 percent by count, "tonight"). Not used as a few-shot example |
| scriptstats-v3-b/run-5.md | bad | reason not given by the owner. Not used as a few-shot example |
| scriptstats-strict-c/run-2.md | bad | owner: knife, bugs and other words that do not relax (H11 candidate; the script has "beetle", "night insects" and "blade" for grass) |
| scriptstats-strict-c/run-11.md | bad | owner: the topic is not concrete (H4: topic "The Slow Moons", generic cosmos imagery, "moon" once) |
| scriptstats-strict-c/run-12.md | bad | owner: repeated suffix (H1 21 percent by the word list; below the old threshold) |
| scriptstats-strict-c/run-13.md | bad | owner: repeated suffix (H1 42 percent) |
| scriptstats-strict-c/run-14.md | good | no reason given. H1 38 percent by the word list, so filler tails at this level did not make the owner reject |

## 5. Preferences (reported next to the verdict, never reject)

- P1 One unifying motif and one night that progresses.
- P2 The script fits its style. Myth: sleeping dragon, gentle giant, enchanted
  well, old lullaby. Earthside: concrete places and materials (stone, moss, soil,
  bark). Cosmos: concrete named objects (Titan, Enceladus, a ring gap), not
  generic starlight.
- P3 The narrator sometimes speaks to the listener ("you"). Based on one label,
  so the weakest preference.
- P4 New images arrive in every paragraph.
- P5 Reads cleanly aloud: no tongue-tangling endings. Changed in v3.2: it used
  to say "short sentences". One listener, one voice and one trial script found
  longer sentences (22 to 35 words) with soft endings that differ from each
  other better for sleep. A hypothesis, not a finding.
- P6 Word density: "soft", "gentle", "quiet", "calm", "tender", "blanket", "veil"
  together stay under 6 percent of words. A motif word (for example "lantern")
  stays under 3 percent.
- P7 Under review (v3.2): "sentences stay under about 20 words" came from the
  generator prompt and from Sleepy, not from listening. Listening found the
  rule made scripts sound like a metronome. It is not applied until a fresh
  batch tests it.
- P8 Length fits the target video. A script that is too short for a long sleep
  video is looped or extended.

## 6. Decision rule

1. Run the code checks, then the judge checklist.
2. A script is **bad** if any hard defect reaches its reject threshold.
3. Combined rule: the distinct sentences flagged by all warnings and below
   threshold defects together must fit a small edit (section 1). If they do not,
   the script is **bad**.
4. If defects exist but the combined rule holds, the verdict is **good after
   small edit**, and the report lists the quoted sentences to fix.
5. Otherwise **good**.
6. Preferences are printed beside the verdict and never change it.

### 6a. Two modes for an unsure judge

| mode | when | unsure means |
|---|---|---|
| owner reviews | today: the owner reads every script | `present: false`; a missed defect is caught in review |
| autonomous | once approval is removed | `unsure: true`, the script goes to NEEDS_REVIEW |

Why they differ: a false rejection costs one regeneration. A false acceptance
costs a bad video that is published. Once nobody reads every script, the
cheaper error is the one to prefer.

## 7. Output format

Code checks return `{id, count, share, quote}`.
The judge returns one object per defect H2b to H10 (H5 split in three):

```json
{"id": "H5b", "present": true, "unsure": false, "markers": [{"par": 3, "quote": "..."}, {"par": 7, "quote": "..."}], "quote": "..."}
```

`present: true` without a verbatim quote counts as not found.

Final report:

```json
{"verdict": "good|good_after_small_edit|bad|needs_review", "rejecting": ["H1"], "warnings": ["H2"], "fix_quotes": ["..."], "preferences": {"P3": false}}
```

## 8. Judge prompt (drop-in)

```
You check a sleep narration script against a list of defects. You do not give opinions or scores.
For each defect id below, answer present true or false, in order, in one JSON list.
Rules: default is false. present true needs a verbatim quote from the script. For H5a, H5b, H6 and
H10, first list the markers you found with paragraph numbers, then decide. If unsure, set unsure true
and present false. Never rewrite the script or suggest changes. Return only the JSON list.
Defects: H2b, H5a, H5b, H5c, H6, H7, H10 (definitions and allowed exceptions follow).
```

## 9. Limits and validation plan

- One generator model, one prompt family, small samples. Half of the 42 labels
  are Claude's and count less than the owner's.
- Thresholds (H1, H2, H3, H7, small edit) were chosen by looking at those
  labels. Validate on at least 30 new scripts the owner labels without seeing the
  checker output.
- First held-out test (strict-c, 16 generated, 13 scripts with a label, owner
  labelled before any checker output): the code checks flagged 4 of 8 owner-good
  scripts (all H1), so the success criterion "no check flags a good script"
  failed. 2 of 4 owner-bad scripts passed (upper bound 85 percent). The gpt-oss
  checklist, one judge and one run, added no finding to any of the 13 scripts and
  did not catch either of the two. n is tiny; it does not show whether the
  checklist ever fires, which the anchor test must show first.
- The second judge (qwen3.8-27b) could not run: the free tier allows 1000 output
  tokens per minute and one checklist reply asks for more.
- Anchor test: run the judge on the anchors that have an owner reason (7 above).
  It must match all of them. A miss means the definition is unclear, so fix the
  wording and not the threshold. This is a regression test, not validation: the
  spec was written from these scripts.
- Track false rejects (checker bad, owner good) and misses (checker good, owner
  bad) separately. Under the owner reviews mode, keep false rejects near zero.
  Under the autonomous mode, misses are the number that matters.
- New defects come only from labelled scripts with the owner's reason attached.
- Labelling is hard, and that is a result. Direct good or bad labels did not
  hold up, the owner has no time to read and label scripts of 600 words, and
  27 strict-d labels were written by Claude (tag by-claude, weaker). The
  owner's own consistency is not measured. To measure it, the owner labels a
  random sample of about 6 strict-d scripts before seeing Claude's labels.
- strict-d (27 scripts, 12 good and 15 bad by Claude's labels, one generator
  model, one judge, single runs): the code checks let through 13 of 15 bad
  scripts (upper bound 96 percent); code plus the checklist let through 9 of 15
  (upper bound 80 percent); 0 of 12 good were rejected (upper bound 24 percent).
  The success criterion "report the let-through rate with its bound" is met, the
  rate itself is high.
- Judge repeat measurement (10 hard scripts chosen from strict-a and strict-d, 3
  runs each, gpt-oss-120b at low effort): 2 of 10 scripts disagreed with
  themselves (upper bound 51 percent). A hard-case subset, so not a typical
  rate.
- Confirm pass finding: it kept a defect only if the second pass repeated one of
  the first pass's quotes. In strict-d1/run-2 a real time regression (night,
  then sunrise and morning mist, then night) was reported in 4 of 4 runs and
  kept in 0 of 4. Option `CONFIRM=presence` keeps a defect when the second pass
  also says it is present: on the 10 hard scripts, 3 runs each, it recovered 2
  bad scripts, rejected 0 of 4 good, and disagreement fell to 1 of 10 (upper
  bound 40 percent). It was designed on one of those scripts, so it is a
  regression check, not a validation. The default stays unchanged.


## Log

- v3.2 (draft, 2026-11-08): the owner listened to four strict-d scripts and to
  one trial script. New candidate fifth question, listening flow (section 0),
  with no check behind it. P5 and P7 reworded because they contradicted what
  the owner heard. Section 9 gets the strict-d result, the judge repeat
  measurement and the confirm pass finding. No threshold changed.

- v3.1 (draft, 2026-11-06): H1 reject threshold set to 60 percent, provisional,
  chosen before the strict-d labels. Reason: with the reject removed, the anchor
  strict-a/run-7 (filler on every sentence, owner-bad) passed the code checks.
  60 sits between the highest owner-good share seen (38 percent) and 100 percent.
  Nothing between 42 and 100 percent has been measured. Validated on strict-d.
- v3 (draft, 2026-10-27): the owner's four questions are the definition of good
  (section 0) and the defects are their evidence. Labelling with scores and a
  decision is proposed (section 1b). H1 loses its reject threshold: on strict-c
  it flagged 4 of 8 owner-good scripts, and the owner-bad run-12 sat at 21
  percent, below it. H1 now warns only; a new threshold needs a new held-out
  batch. New candidate H11 (unsettling words) from strict-c/run-2, warn only.
  strict-c/run-11 added as an H4 anchor. No threshold in this version was set by
  looking at strict-c labels; the H1 change removes one.
- v1, 2026-10-24: first written definition.
- v2: per-defect reject thresholds, small edit defined, judge rules, H5 split,
  H10, structural H1 signal, incompatible season sets, drop-in judge prompt,
  P6 to P8.
- v2.2: H4 can no longer reject by counting words. Measured on the 42 labels: the key noun appears 0 or 1 times in 2 scripts that were called good (strict-a/run-8 and strict-b/run-16, both library scripts), so a count threshold gave false alarms. Code reports a candidate and the judge decides.
- v2.1: small edit widened to 5 sentences or 10 percent (the owner's label of
  strict-a/run-4 needed it); H3(a) and H7 get counts instead of "any" (H7 stays stricter at 3 on purpose; the
  count thresholds for H2, H2b and H3(a) are 6, the first count above a small edit); new H2b (dangling modifier); combined rule added; anchors
  given by file with reasons and without titles; paragraph defined; title rule
  reduced to a warning; two modes for an unsure judge; one call per script with a
  confirmation pass for positives only.
