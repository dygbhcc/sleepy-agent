# Failure log

Every day something breaks. This file keeps the evidence: what I expected,
what happened, why, and what I changed. Entries include mistakes caused by my
own tests and assumptions, because those are the most useful ones.

| Day | What broke | Cause | Fix |
| --- | --- | --- | --- |
| D2 | `gofmt` rejected my first `Mock` struct (field alignment) | I wrote it by hand and never ran the formatter | `make lint` now fails on unformatted code, and CI runs it |
| D2 | The naive pipeline accepts a 5 word script and a script that repeats one sentence 60 times (pinned by `TestKnownGap*`) | No QA gate exists yet. In Sleepy these were the first real failures, found only by reading the output | Planned for D7: QA gates, and those tests flip to expect a rejection |
