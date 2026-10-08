# sleepy-agent: working rules

These rules apply to every contributor and every AI assistant working in this repo.

1. **English only inside the repo.** Identifiers, comments, log and error messages, test names, commit messages and docs are always written in English. Never Turkish, no exceptions.
2. **No agent frameworks.** The point of the project is to build the agent loop from scratch. Standard library first. Any other dependency needs a stated reason, and API client libraries (for example Google's) stay inside their own adapter package.
3. **Everything goes through `llm.Provider`.** Tests use the mock provider and never need a network or an API key.
4. **Never commit secrets.** No API keys, tokens, `.env` files or databases. The repo is public from day one.
5. **Before every commit** run `make lint` and `make test`.
6. **Log every failure** in `FAILURE_LOG.md`, including mistakes caused by our own tests.
7. **No public publishing without an autonomy policy.** The publisher defaults to dry-run. Real uploads are unlisted and need approval until an explicit policy says otherwise.

## Layout

- `internal/agent`: agent loop, tools, `Decider`. `internal/llm`: `Provider`, mock, Groq client.
- `internal/domain`, `internal/runner`, `internal/store`: the run state machine, worker loop and storage.
- `internal/pipeline`: the naive Day 2 generator, temporary. `cmd/`: demos.

## Conventions

- A status names the last step that finished. The step that runs while a run sits in a status is chosen by `runner.buildStages`. `UPLOADED` means "packaged, upload still to run" (inherited from Sleepy).
- A worker iteration claims a run, does exactly one step, and releases it. Never add a step that does two things.
- The `Decider` is consulted before every step. Unknown answers mean stop. Never add implicit permission.
- Mocks sit next to their interface in `mock.go`, are safe for concurrent use, and have `FailNext` and call counters.
- A ported package says so in its package comment.
- When a test passes the first time, break the code on purpose and check that the test fails.

