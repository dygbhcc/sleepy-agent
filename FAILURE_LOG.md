# Failure log

Every day something breaks. This file keeps the evidence: what I expected,
what happened, why, and what I changed. Entries include mistakes caused by my
own tests and assumptions, because those are the most useful ones.

| Day | What broke | Cause | Fix |
| --- | --- | --- | --- |
| D2 | `gofmt` rejected my first `Mock` struct (field alignment) | I wrote it by hand and never ran the formatter | `make lint` now fails on unformatted code, and CI runs it |
| D2 | The naive pipeline accepts a 5 word script and a script that repeats one sentence 60 times (pinned by `TestKnownGap*`) | No QA gate exists yet. In Sleepy these were the first real failures, found only by reading the output | Planned for D7: QA gates, and those tests flip to expect a rejection |
| D3 | First real call to Groq returned HTTP 404: the hardcoded default model `llama-3.3-70b-versatile` was not available to my key | Model names change and I baked one into the provider. Mocks and `httptest` could not catch it | Removed the default model, `GROQ_MODEL` is now required with a key |
| D3 | `openai/gpt-oss-20b` and `openai/gpt-oss-120b` both returned HTTP 400 "Tool choice is none, but model called a tool" on the first call, 0 tool calls completed | The gpt-oss models are trained for native tool calling. They try a native call when my prompt lists tools in text, and the request carries no `tools`. My first fix (JSON mode) did not help: same error on both sizes | Not fixed in the protocol. `qwen/qwen3.8-27b` completed the same task: 3 tool calls, 1445 prompt and 203 completion tokens. Native `tools` support is a later decision |
| D4 | `Worker.Drain` looped for 78 seconds and had to be killed when I broke the store on purpose so it handed back a run that was waiting for approval | `Drain` trusted the store to make progress. Found by a mutation check of my own tests, not in normal use | `Drain` now stops with an error after `MaxDrainSteps`, with a test that uses a store that forgets approvals |
