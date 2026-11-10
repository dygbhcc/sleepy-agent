# Autonomy policy (Day 10)

What the pipeline may do without a human, and what it has to show before that
grows. The code decides; the policy file only asks.

## Levels

| Level | Meaning |
| --- | --- |
| 0 `halt` | Every step is stopped. The kill switch, and what an invalid policy means. |
| 1 `review` | Every step runs. Publishing waits for a human's yes. The default. |
| 2 `unlisted` | An unlisted publish goes without a human's yes. Needs evidence, see below. |

There is no level for public publishing. The worker tells the publisher the
widest visibility it may use (`unlisted`) and fails the run, without a retry,
if the publisher reports anything but `dry-run` or `unlisted`. That check
runs after the publisher acted, so it names the publish in the error for a
human to take down. It cannot undo it.

Besides the level, `runner.AutonomyGate` has an allow-list: a call to anything
that is not one of `runner.Stages` is stopped. If the run cannot be read when
publishing, the answer is stop.

## What level 2 needs

The policy file (`autonomy.json`) asks for a level and records the latest
measurement of the quality checks against labels, as counts. Code computes the
bound itself and compares it with constants that a file cannot change
(`internal/policy/autonomy.go`):

- the labels are the owner's, not a model's;
- at least 30 bad scripts were measured;
- the upper end of the 95 percent Wilson interval for "a bad script passed the
  checks" is at most 10 percent.

If any of the three is missing the gate behaves as level 1 and says why in the
event and in `make skeleton`.

## Where we stand

The only measurement of that kind (Day 9, strict-d) has 15 bad scripts, labels
written by Claude, and 9 of the 15 passed the checks (upper bound 80 percent;
one generator model, one judge, single runs). Asking for level 2 on those numbers
is refused on all three counts. `autonomy.json` therefore asks for level 1.
Level 2 is reachable only after a batch the owner labels, at least 30 bad
scripts, and a let-through rate that the bound supports. With nothing let
through, 0 of 30 gives an upper bound of 11.4 percent, which is above 10, and
0 of 35 gives 9.9 percent. So the bar is in practice 35 bad scripts with none
let through, or more scripts with a few.

## Not built

- A daily publish budget and a spend limit. Attempts per step are limited
  already (`Config.MaxAttempts`); money is not, because no step spends any yet.
- Approval that expires, or that is tied to the file that was approved.
- Evidence that expires. The policy records when it was measured, code does not
  look at it.
- Autonomy for steps other than publishing. Voice and render will need their
  own checks (Day 11).
