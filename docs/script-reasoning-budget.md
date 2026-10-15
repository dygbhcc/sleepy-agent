# gpt-oss-120b: token budget and reasoning effort

Measured with `make script`, model `openai/gpt-oss-120b` on Groq, style
Earthside, 5 minute episodes (limits 500 to 800 words), topics Rings of Saturn,
Velvet Meadow, The Slow Moons. 3 runs per setting, one attempt per run, no
retry. One model and one prompt, so this is a measurement, not a claim about
the model in general.

| Setting | Script returned | Empty reply | Gate accepted |
|---|---|---|---|
| default budget (1500 tokens) | 0 of 3 | 3 of 3 | 0 of 3 |
| 4000 token budget | 0 of 3 | 3 of 3 | 0 of 3 |
| `reasoning_effort` low, 1500 tokens | 2 of 3 | 1 of 3 | 1 of 3 |
| `reasoning_effort` low, 4000 tokens | 3 of 3 | 0 of 3 | 1 of 3 |

What the empty replies looked like: `finish_reason: length`, 0 bytes of text,
1500 completion tokens of which 1498 were reasoning (4000 of which 3998 with the
larger budget). Average tokens per run (prompt plus completion, failed runs
included): 2139 at the default, 4639 at 4000, 2211 with low effort, 4419 with
low effort and 4000.

What the scripts that did come back looked like: word counts 510 and 623 (gate
accepted), 479 and 446 (rejected, too short), and 52 words ending mid sentence
(rejected, too short). The 52 word script looks cut off by the token limit, but
`finish_reason` was not checked when a reply had text, so that was not confirmed.
The generator now fails a chunk whose `finish_reason` is `length` (see
FAILURE_LOG.md), so a cut off script like this one is reported as a truncation
instead of reaching the gate. The numbers in the table were measured before that
change.

Not measured: why the model reasons up to the limit with this prompt (the
system prompt ends with a self check instruction, which is a guess and has not
been tested), whether `medium` or `high` effort behave differently, and whether
the Velvet Meadow topic is harder (it failed in both low effort settings, 2
observations).

Reproduce:

```bash
GROQ_MODEL=openai/gpt-oss-120b RUNS=3 make script
GROQ_MODEL=openai/gpt-oss-120b RUNS=3 MAX_TOKENS=4000 make script
GROQ_MODEL=openai/gpt-oss-120b RUNS=3 REASONING_EFFORT=low make script
GROQ_MODEL=openai/gpt-oss-120b RUNS=3 REASONING_EFFORT=low MAX_TOKENS=4000 make script
```
