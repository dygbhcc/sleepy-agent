# Contributing to sleepy-agent

Thanks for looking. This is a learning project built in public, so small, well
explained changes are welcome. Please read these rules before opening a PR.

## The rules

1. **English only.** Identifiers, comments, log and error messages, test names,
   commit messages and docs are always English.
2. **No agent frameworks.** The point is to build the agent loop from scratch.
   Standard library first. A new dependency needs an issue and a reason first.
3. **Everything goes through `llm.Provider`** (and later `Voice`, `Renderer`,
   `Publisher`). Tests use the mocks. A test must never need a network, an API
   key or a paid service.
4. **No secrets.** Never commit API keys, tokens, `.env` files or databases.
   The repo is public.
5. **Nothing goes public without an autonomy policy.** The publisher defaults
   to dry-run. Real uploads are unlisted and need approval.
6. **Run `make lint` and `make test` before you push.** CI runs the same two
   commands and must be green.
7. **Log failures.** If your change exposes or fixes a failure, add a row to
   `FAILURE_LOG.md`, including mistakes caused by your own tests.

## How to contribute

1. Open an issue first for anything bigger than a typo or a small fix.
2. Fork, create a branch, keep the change small and focused.
3. Add or update tests. Prefer deterministic tests with the mock provider.
4. Open a PR using the template. Explain what problem it solves and how you
   checked it.

## Review

- The maintainer reviews every PR. Code owners must approve.
- Direct pushes to the default branch are blocked for everyone else.
- AI assisted code is fine. Say so in the PR, and make sure you understand and
  have tested every line you submit.
- PRs that add an agent framework, hide behavior in prompts that should be code,
  or weaken a safety default will be closed with an explanation.

## Reporting a security problem

Do not open a public issue. See [SECURITY.md](SECURITY.md).
