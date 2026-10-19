package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sleepy-agent/internal/fix"
	"sleepy-agent/internal/llm"
	"sleepy-agent/internal/policy"
	"sleepy-agent/internal/qa"
	"sleepy-agent/internal/runner"
	"sleepy-agent/internal/script"
)

// The paired test answers the question the two-arm run cannot: from the SAME
// rejected script, how often does one more blind attempt pass, and how often
// does one directed fix pass?
//
// A first attempt is generated per topic. Only a script the gate rejects on a
// QA reason becomes a starting point (accepted scripts, truncations and errors
// are counted and skipped). From each starting point, TRIALS blind attempts
// (the same request again) and TRIALS directed attempts (the fix engine's first
// plan for that failure) are made, alternating. Each trial is one attempt, so
// the numbers are recovery in one step, not in three.
//
// The trials of one starting point share one failing script, so they are not
// independent draws: read the number of starting points before the number of
// trials.

type trial struct {
	pass    bool
	cured   bool   // the original failure is gone, even if the gate found another
	outcome string // pass, a FailType, truncated or error
	words   int
}

type start struct {
	topic    string
	failType qa.FailType
	words    int
	plan     fix.Plan
	hasPlan  bool
	blind    []trial
	directed []trial
}

type pairedResult struct {
	firstAttempts int
	accepted      int
	unusable      int // truncated or error on the first attempt
	starts        []start
}

func judge(pol policy.Policy, sc runner.Script, err error, ft qa.FailType) trial {
	if err != nil {
		if errors.Is(err, script.ErrTruncated) {
			return trial{outcome: "truncated"}
		}
		return trial{outcome: "error"}
	}
	rep := qa.Script(sc.Title, sc.Body, pol)
	t := trial{words: len(strings.Fields(sc.Body)), pass: rep.Pass, outcome: "pass", cured: fix.Fixed(rep, ft)}
	if !rep.Pass {
		t.outcome = string(rep.FailType)
	}
	return t
}

func paired(ctx context.Context, out io.Writer, p llm.Provider, cfg script.Config, firstAttempts, trials int, pause time.Duration, outDir string) (pairedResult, error) {
	ctr := &counter{inner: p}
	sc := runner.LLMScripter{Provider: ctr, Config: cfg}
	pol := policy.ForDuration(cfg.DurationMin)
	engine := fix.NewEngine()
	var res pairedResult
	first := true
	wait := func() {
		if !first {
			time.Sleep(pause)
		}
		first = false
	}

	for i := 1; i <= firstAttempts; i++ {
		topic := topics[(i-1)%len(topics)]
		wait()
		rctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
		s0, err := sc.Script(rctx, topic)
		cancel()
		res.firstAttempts++
		if err != nil {
			res.unusable++
			fmt.Fprintf(out, "%-26s first attempt unusable (%s), skipped\n", topic, judge(pol, s0, err, "").outcome)
			continue
		}
		rep := qa.Script(s0.Title, s0.Body, pol)
		if rep.Pass {
			res.accepted++
			fmt.Fprintf(out, "%-26s first attempt accepted, nothing to repair\n", topic)
			continue
		}
		st := start{topic: topic, failType: rep.FailType, words: len(strings.Fields(s0.Body))}
		st.plan, st.hasPlan = engine.Decide(rep, nil)
		if !st.hasPlan {
			fmt.Fprintf(out, "%-26s %s, no fix in the catalog, skipped\n", topic, rep.FailType)
			continue
		}
		fmt.Fprintf(out, "%-26s %s at %d words, fix %s, %d trials per arm\n", topic, rep.FailType, st.words, st.plan.ID, trials)
		for k := 0; k < trials; k++ {
			wait()
			rctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
			b, berr := sc.Script(rctx, topic)
			cancel()
			st.blind = append(st.blind, judge(pol, b, berr, st.failType))
			wait()
			rctx, cancel = context.WithTimeout(ctx, 15*time.Minute)
			d, derr := sc.ScriptAdjusted(rctx, topic, st.plan.Adjust(s0.Title, s0.Body))
			cancel()
			st.directed = append(st.directed, judge(pol, d, derr, st.failType))
			fmt.Fprintf(out, "    trial %d: blind %s %dw | directed %s %dw\n", k+1,
				st.blind[k].outcome, st.blind[k].words, st.directed[k].outcome, st.directed[k].words)
		}
		res.starts = append(res.starts, st)
		savePaired(outDir, i, st, s0)
	}
	return res, nil
}

func count(ts []trial, f func(trial) bool) int {
	n := 0
	for _, t := range ts {
		if f(t) {
			n++
		}
	}
	return n
}

func pairedReport(out io.Writer, r pairedResult, label string, trials int) {
	fmt.Fprintf(out, "\nmodel: %s, %d trials per arm per starting point, one attempt each\n", label, trials)
	fmt.Fprintf(out, "first attempts: %d, accepted at once: %d, truncated or error: %d, rejected by the gate and tested: %d\n\n",
		r.firstAttempts, r.accepted, r.unusable, len(r.starts))
	if len(r.starts) == 0 {
		fmt.Fprintln(out, "No rejected first attempt to start from, so nothing was compared. Run again or raise RUNS.")
		return
	}
	fmt.Fprintln(out, "| starting point | failure | words | fix | blind: passed | directed: passed | blind: failure gone | directed: failure gone |")
	fmt.Fprintln(out, "|---|---|---|---|---|---|---|---|")
	var bp, dp, bc, dc, n int
	for _, s := range r.starts {
		pass := func(t trial) bool { return t.pass }
		cured := func(t trial) bool { return t.cured }
		a, b, c, d := count(s.blind, pass), count(s.directed, pass), count(s.blind, cured), count(s.directed, cured)
		fmt.Fprintf(out, "| %s | %s | %d | %s | %d/%d | %d/%d | %d/%d | %d/%d |\n", s.topic, s.failType, s.words, s.plan.ID,
			a, len(s.blind), b, len(s.directed), c, len(s.blind), d, len(s.directed))
		bp, dp, bc, dc, n = bp+a, dp+b, bc+c, dc+d, n+len(s.blind)
	}
	fmt.Fprintf(out, "| **total** | | | | **%d/%d** | **%d/%d** | **%d/%d** | **%d/%d** |\n", bp, n, dp, n, bc, n, dc, n)
	fmt.Fprintf(out, "\nThese %d trials per arm come from %d failing scripts. Trials from one script are not independent, so the number of starting points is the real sample size. \"Failure gone\" counts a trial whose original failure is fixed even if the gate found another one.\n", n, len(r.starts))
}

func savePaired(dir string, i int, s start, s0 runner.Script) {
	if dir == "" {
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	var b strings.Builder
	fmt.Fprintf(&b, "topic: %s\nstart: %s at %d words\nfix: %s\n\n", s.topic, s.failType, s.words, s.plan.ID)
	for k := range s.blind {
		fmt.Fprintf(&b, "trial %d\n  blind:    %s, %d words\n  directed: %s, %d words\n", k+1, s.blind[k].outcome, s.blind[k].words, s.directed[k].outcome, s.directed[k].words)
	}
	fmt.Fprintf(&b, "\nrejected script the trials started from:\n\n%s\n", s0.Body)
	_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("%02d-paired.md", i)), []byte(b.String()), 0o644)
}
