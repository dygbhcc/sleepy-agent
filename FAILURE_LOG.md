# Failure log

Every day something breaks. This file keeps the evidence: what I expected,
what happened, why, and what I changed. Entries include mistakes caused by my
own tests and assumptions, because those are the most useful ones.

| Day | What broke | Cause | Fix |
| --- | --- | --- | --- |
| D2 | `gofmt` rejected my first `Mock` struct (field alignment) | I wrote it by hand and never ran the formatter | `make lint` now fails on unformatted code, and CI runs it |
