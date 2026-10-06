# sleepy-agent: working rules

These rules apply to every contributor and every AI assistant working in this repo.

1. **English only inside the repo.** Identifiers, comments, log and error messages, test names, commit messages and docs are always written in English. Never Turkish, no exceptions.
2. **No agent frameworks.** The point of the project is to build the agent loop from scratch. Standard library first.
3. **Everything goes through `llm.Provider`.** Tests use the mock provider and never need a network or an API key.
4. **Never commit secrets.** No API keys, tokens, `.env` files or databases. The repo is public from day one.
5. **Before every commit** run `make lint` and `make test`.
6. **Log every failure** in `FAILURE_LOG.md`, including mistakes caused by our own tests.
7. **No public publishing without an autonomy policy.** The publisher defaults to dry-run. Real uploads are unlisted and need approval until an explicit policy says otherwise.
