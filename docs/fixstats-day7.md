# Day 7: blind retry vs directed fix (one run, 2026-10-19)

Command: `GROQ_MODEL=openai/gpt-oss-120b REASONING_EFFORT=low MAX_TOKENS=4000 RUNS=8 make fixstats`
Model: `openai/gpt-oss-120b`, reasoning low, 4000 tokens per chunk, 5 minute episodes, Earthside, 8 topics per arm, at most 3 attempts per run.

The arms draw separate first attempts. They never saw the same failure, so this is a baseline and not a comparison.

| arm | runs | first attempt accepted | first attempt not accepted | of those, recovered | parked | avg attempts |
|---|---|---|---|---|---|---|
| blind | 8 | 5 | 3 | 3/3 | 0 | 1.5 |
| directed | 8 | 5 | 3 | 3/3 | 0 | 1.6 |

## What the records show (out/fixstats, not committed)

- Blind: 3 first attempts were `WORDCOUNT_LOW` (440, 472, 485 words against a 500 minimum). All 3 were accepted after the same request again: 2 on attempt 2, 1 on attempt 3 (attempt 2 had a banned phrase).
- Directed: 3 first attempts were truncated (`finish_reason: length`, 4000 completion tokens of which 3998 were reasoning). Truncation is a plain retry by design, the fix engine does not act on it.
- Directed, after a truncation and a plain retry: 2 runs were `WORDCOUNT_LOW` (496 and 489 words). The fix `wc_low_continue` ran in both, and both were accepted on attempt 3 (671 and 670 words). That is 2 fixes, 2 recoveries.

## What this does not show

- That a directed fix beats a blind retry. Blind recovered 3 of 3 as well.
- Why the directed arm had 3 truncations in 8 and the blind arm 0 in 8. N is too small to say.
