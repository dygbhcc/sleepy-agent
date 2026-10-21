# Day 8: what the QA gate does not see (one measurement, 2026-10-21)

Input: the 16 scripts saved by the Day 7 `make fixstats` run (`out/fixstats`, not committed).
Model: `openai/gpt-oss-120b`, reasoning low, 4000 tokens per chunk, Earthside, 5 minute episodes. All 16 were accepted by the QA gate.
Command: `go run ./cmd/evalstats out/fixstats` (no network, no key). The thresholds are a first guess (`eval.DefaultThresholds`), not Sleepy's.

## Result

- 5 of the 16 accepted scripts copy the prompt's own imagery lines word for word, 22 to 46 times. The prompt gives the model 10 lines to rotate through, so 10 is one use each. The other 11 scripts copy them 0 to 10 times.
- The same 5 have 13% to 30% of their 4-word phrases repeating, against 0% to 5% for the others.
- Reading one of them (`08-directed`, topic "The Quiet Library"): nearly every sentence carries one of the forest imagery lines ("like dew forming on cool morning grass", "slow river bending through quiet meadow") inside a script about a library.
- The gate accepted all of them. Its repetition check is a minimum unique word ratio of 0.12, and these scripts measure 0.28 to 0.55.
- Sentence length was not the problem in this data: 0% to 2% of sentences were under 8 words and none over 20.
- Two scripts were extended by a continuation. In `02-directed` the added 175 words use 25% new content words and 10% of their 4-word phrases already occur in the script; in `07-directed` the added 181 words use 39% new content words and 1% repeat.

## Limits

- 16 scripts, one model, one setting, one style. Two continuations are not a sample.
- The cause is not established. Some topics do not match the Earthside imagery (for example "Rings of Saturn"), which may push the model to paste the lines; that is a guess.
- The metrics count repetition and copying. They do not judge whether a script is good. Whether a human, or the shadow judge, would reject these five is not measured yet (`LABELS` and `JUDGE=1` in `cmd/evalstats` exist for that).

## Per script

| script | arm | words | under 8 words | repeated 4-grams | unique words | prompt lines copied |
|---|---|---|---|---|---|---|
| 01-blind | blind | 533 | 0% | 12.6% | 0.46 | 22 |
| 01-directed | directed | 548 | 2% | 17.6% | 0.43 | 22 |
| 02-blind | blind | 542 | 0% | 1.5% | 0.55 | 0 |
| 02-directed | directed | 671 | 2% | 5.1% | 0.46 | 1 |
| 03-blind | blind | 581 | 0% | 0.7% | 0.50 | 0 |
| 03-directed | directed | 526 | 0% | 0.0% | 0.58 | 0 |
| 04-blind | blind | 611 | 0% | 1.6% | 0.41 | 0 |
| 04-directed | directed | 653 | 0% | 0.6% | 0.45 | 0 |
| 05-blind | blind | 624 | 0% | 0.6% | 0.46 | 0 |
| 05-directed | directed | 553 | 0% | 0.0% | 0.51 | 0 |
| 06-blind | blind | 705 | 0% | 22.5% | 0.28 | 46 |
| 06-directed | directed | 506 | 0% | 0.8% | 0.55 | 10 |
| 07-blind | blind | 557 | 0% | 0.0% | 0.54 | 10 |
| 07-directed | directed | 670 | 0% | 16.6% | 0.37 | 34 |
| 08-blind | blind | 508 | 0% | 0.4% | 0.51 | 0 |
| 08-directed | directed | 624 | 0% | 29.5% | 0.30 | 45 |
