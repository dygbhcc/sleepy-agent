# Native vs prompt based tool calling

Measured on 2026-10-08 with `make compare`, on Groq, one task (the Day 3
draft check: three tools must run, then a verdict). 5 runs per mode, 15 second
pause between runs, rate limit errors waited out and counted.

This is one task and 5 runs per mode. It shows that tool calling is a per model
decision. It is not a reliability claim.

Reproduce:

```
export GROQ_API_KEY=...
GROQ_MODEL=openai/gpt-oss-20b COMPARE_RUNS=5 make compare
GROQ_MODEL=openai/gpt-oss-120b COMPARE_RUNS=5 make compare
GROQ_MODEL=qwen/qwen3.8-27b COMPARE_RUNS=5 make compare
```

"Completed" means the run finished and all three tools actually ran.

## openai/gpt-oss-20b

| mode | completed | API errors | not completed | invalid replies | rate-limit retries | avg tool calls | avg tokens (prompt+completion) |
|---|---|---|---|---|---|---|---|
| prompt | 0/5 | 5 | 0 | 0 | 0 | 0.0 | 0 |
| native | 5/5 | 0 | 0 | 0 | 0 | 3.0 | 2108 |

Prompt mode errors: 5 x `HTTP 400: Tool choice is none, but model called a tool`.

## openai/gpt-oss-120b

| mode | completed | API errors | not completed | invalid replies | rate-limit retries | avg tool calls | avg tokens (prompt+completion) |
|---|---|---|---|---|---|---|---|
| prompt | 0/5 | 5 | 0 | 0 | 0 | 0.0 | 0 |
| native | 5/5 | 0 | 0 | 0 | 0 | 3.0 | 2046 |

Prompt mode errors: 3 x `HTTP 400: Tool choice is none, but model called a tool`,
2 x `HTTP 400: Failed to validate JSON`.

## qwen/qwen3.8-27b

| mode | completed | API errors | not completed | invalid replies | rate-limit retries | avg tool calls | avg tokens (prompt+completion) |
|---|---|---|---|---|---|---|---|
| prompt | 5/5 | 0 | 0 | 0 | 0 | 3.0 | 1718 |
| native | 5/5 | 0 | 0 | 0 | 1 | 3.0 | 3241 |

Native used about 1.9x the tokens per run. The cause is not measured.

## An earlier, invalid run

The first Qwen comparison ended with 5 of 5 native runs and 1 of 5 prompt runs
as HTTP 429. The harness fired runs back to back and counted a rate limit as a
model failure, so that table measured request rate, not the adapters. It is
excluded here. See `FAILURE_LOG.md`.
