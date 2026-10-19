// Command fixstats measures whether a directed fix recovers a rejected script
// better than asking the model the same question again.
//
// For every topic it runs the real worker twice, on the same model and the same
// QA gate, with three attempts each:
//
//   - blind:    a rejected attempt is repeated as the same request (Day 6);
//   - directed: a rejected attempt gets the fix engine's plan for the failure
//     code, such as a continuation for a short script or a repair note that
//     names the banned words (Day 7).
//
// The arms are interleaved topic by topic, so a slow hour on the provider hits
// both. They do NOT see the same samples: the first attempt of each arm is a
// separate draw. The numbers therefore compare two populations of a few runs,
// not two answers to the same question, and with the default 6 topics a
// difference of one or two runs is noise. What the tool gives is the baseline
// (how often does a blind retry recover, per failure) and a reason to run more.
//
// Without GROQ_API_KEY it plays a scripted mock that writes a short script
// unless it is steered. That only proves the harness works, it measures no
// model. With a key, GROQ_MODEL is required. MAX_TOKENS, REASONING_EFFORT,
// DURATION, STYLE and SCRIPT_PAUSE_SECONDS work as in cmd/scriptstats, RUNS is
// the number of topics.
//
// PAIRED=1 runs the paired test instead (paired.go): from each script the gate
// rejects, TRIALS blind retries against TRIALS directed fixes, one attempt each.
// RUNS is then the number of first attempts to try, not the number of topics.
//
// Every run is written to out/fixstats/ with its attempts and final script.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"sleepy-agent/internal/agent"
	"sleepy-agent/internal/domain"
	"sleepy-agent/internal/fix"
	"sleepy-agent/internal/llm"
	"sleepy-agent/internal/policy"
	"sleepy-agent/internal/qa"
	"sleepy-agent/internal/runner"
	"sleepy-agent/internal/script"
	"sleepy-agent/internal/store"
)

var topics = []string{
	"Rings of Saturn",
	"Velvet Meadow",
	"The Slow Moons",
	"Amber Lanterns",
	"Moss and Still Water",
	"A Lighthouse at Dusk",
	"Snow on Sleeping Fields",
	"The Quiet Library",
}

// attempt is one call of the script step, judged by the gate.
type attempt struct {
	kind    string // first, same request, continue, retitle, regenerate with repair note
	words   int
	verdict string // pass, a FailType, or the kind of error
	detail  string
	body    string
}

// result is what one topic did in one arm.
type result struct {
	arm       string
	topic     string
	status    domain.RunStatus
	attempts  []attempt
	calls     int
	tokens    int
	finalBody string
}

func (r result) accepted() bool {
	return r.status != domain.StatusNeedsReview && r.status != domain.StatusFailed
}
func (r result) firstFailure() string {
	if len(r.attempts) == 0 || r.attempts[0].verdict == "pass" {
		return ""
	}
	return r.attempts[0].verdict
}

// recorder wraps the scripter, so every attempt is judged and logged the way
// the worker will judge it. It passes everything else through.
type recorder struct {
	inner runner.LLMScripter
	pol   policy.Policy
	mu    sync.Mutex
	log   []attempt
}

func (r *recorder) Script(ctx context.Context, topic string) (runner.Script, error) {
	kind := "first"
	if r.count() > 0 {
		kind = "same request"
	}
	return r.record(kind, func() (runner.Script, error) { return r.inner.Script(ctx, topic) })
}

func (r *recorder) ScriptAdjusted(ctx context.Context, topic string, a script.Adjust) (runner.Script, error) {
	kind := "regenerate with repair note"
	switch a.Mode {
	case script.ModeContinue:
		kind = "continue"
	case script.ModeRetitle:
		kind = "retitle"
	}
	return r.record(kind, func() (runner.Script, error) { return r.inner.ScriptAdjusted(ctx, topic, a) })
}

func (r *recorder) Fingerprint() string { return r.inner.Fingerprint() }

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.log)
}

func (r *recorder) record(kind string, call func() (runner.Script, error)) (runner.Script, error) {
	sc, err := call()
	a := attempt{kind: kind}
	if err != nil {
		a.verdict = "error"
		if errors.Is(err, script.ErrTruncated) {
			a.verdict = "truncated"
		}
		a.detail = oneLine(err.Error())
	} else {
		rep := qa.Script(sc.Title, sc.Body, r.pol)
		a.words, a.body = len(strings.Fields(sc.Body)), sc.Body
		a.verdict = "pass"
		if !rep.Pass {
			a.verdict, a.detail = string(rep.FailType), rep.Summary()
		}
	}
	r.mu.Lock()
	r.log = append(r.log, a)
	r.mu.Unlock()
	return sc, err
}

func (r *recorder) take() []attempt {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.log
	r.log = nil
	return out
}

// counter adds up the calls and tokens a provider spends.
type counter struct {
	inner  llm.Provider
	mu     sync.Mutex
	calls  int
	tokens int
}

func (c *counter) Name() string { return c.inner.Name() }
func (c *counter) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	resp, err := c.inner.Complete(ctx, req)
	c.mu.Lock()
	c.calls++
	c.tokens += resp.Usage.PromptTokens + resp.Usage.CompletionTokens
	c.mu.Unlock()
	return resp, err
}

func (c *counter) take() (calls, tokens int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	calls, tokens = c.calls, c.tokens
	c.calls, c.tokens = 0, 0
	return
}

// arm is a worker with or without the fix engine, on its own store.
type arm struct {
	name   string
	st     *store.File
	w      *runner.Worker
	rec    *recorder
	ctr    *counter
	engine *fix.Engine // nil for the blind arm
}

func newArm(name string, p llm.Provider, cfg script.Config, directed bool, maxAttempts int) (*arm, error) {
	a := &arm{name: name, st: store.NewMemory(nil), ctr: &counter{inner: p}}
	pol := policy.ForDuration(max(cfg.DurationMin, 5))
	if cfg.DurationMin > 0 {
		pol = policy.ForDuration(cfg.DurationMin)
	}
	a.rec = &recorder{inner: runner.LLMScripter{Provider: a.ctr, Config: cfg}, pol: pol}
	wcfg := runner.Config{WorkerID: name, MaxAttempts: maxAttempts, Policy: pol}
	if directed {
		a.engine = fix.NewEngine()
		wcfg.Fixes = a.engine
	}
	w, err := runner.New(a.st, runner.Steps{
		Scripter: a.rec, Voice: &runner.MockVoice{}, Renderer: &runner.MockRenderer{}, Publisher: &runner.MockPublisher{},
	}, agent.AlwaysAct{}, wcfg)
	if err != nil {
		return nil, err
	}
	a.w = w
	return a, nil
}

func (a *arm) runTopic(ctx context.Context, topic string) (result, error) {
	run, err := a.st.CreateRun(ctx, topic)
	if err != nil {
		return result{}, err
	}
	if err := a.w.Drain(ctx); err != nil {
		return result{}, err
	}
	got, err := a.st.GetRun(ctx, run.ID)
	if err != nil {
		return result{}, err
	}
	calls, tokens := a.ctr.take()
	return result{arm: a.name, topic: topic, status: got.Status, attempts: a.rec.take(), calls: calls, tokens: tokens,
		finalBody: got.Outputs[domain.OutputScript]}, nil
}

// builder makes a fresh provider for one topic of one arm.
type builder func() llm.Provider

// experiment runs every topic through both arms, interleaved.
func experiment(ctx context.Context, out io.Writer, build builder, cfg script.Config, runs, maxAttempts int, pause time.Duration, outDir string) ([]result, *fix.Engine, error) {
	blind, err := newArm("blind", build(), cfg, false, maxAttempts)
	if err != nil {
		return nil, nil, err
	}
	directed, err := newArm("directed", build(), cfg, true, maxAttempts)
	if err != nil {
		return nil, nil, err
	}
	var all []result
	for i := 1; i <= runs; i++ {
		topic := topics[(i-1)%len(topics)]
		for _, a := range []*arm{blind, directed} {
			if len(all) > 0 {
				time.Sleep(pause)
			}
			rctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
			r, err := a.runTopic(rctx, topic)
			cancel()
			if err != nil {
				return all, directed.engine, err
			}
			all = append(all, r)
			fmt.Fprintf(out, "%-8s %-26s %-13s %s\n", r.arm, topic, r.status, describe(r.attempts))
			save(outDir, i, r)
		}
	}
	return all, directed.engine, nil
}

func describe(as []attempt) string {
	parts := make([]string, len(as))
	for i, a := range as {
		w := ""
		if a.words > 0 {
			w = fmt.Sprintf(" %dw", a.words)
		}
		parts[i] = fmt.Sprintf("%d:%s%s %s", i+1, strings.ReplaceAll(a.kind, " with repair note", "+note"), w, a.verdict)
	}
	return strings.Join(parts, " | ")
}

// arms of one kind, for the tables.
func byArm(rs []result, name string) []result {
	var out []result
	for _, r := range rs {
		if r.arm == name {
			out = append(out, r)
		}
	}
	return out
}

type tally struct {
	runs, firstOK, notFirst, recovered, parked, failed int
	calls, tokens, attempts                            int
}

func tallyOf(rs []result) tally {
	var t tally
	for _, r := range rs {
		t.runs++
		t.calls += r.calls
		t.tokens += r.tokens
		t.attempts += len(r.attempts)
		switch {
		case r.firstFailure() == "":
			t.firstOK++
		default:
			t.notFirst++
			if r.accepted() {
				t.recovered++
			}
		}
		switch r.status {
		case domain.StatusNeedsReview:
			t.parked++
		case domain.StatusFailed:
			t.failed++
		}
	}
	return t
}

func avg(total, n int) string {
	if n == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.1f", float64(total)/float64(n))
}

func report(out io.Writer, rs []result, engine *fix.Engine, label string, maxAttempts int) {
	fmt.Fprintf(out, "\nmodel: %s, %d attempts per run at most\n\n", label, maxAttempts)
	fmt.Fprintln(out, "| arm | runs | first attempt accepted | first attempt not accepted | of those, recovered | parked for review | failed | avg attempts | avg model calls | avg tokens |")
	fmt.Fprintln(out, "|---|---|---|---|---|---|---|---|---|---|")
	for _, name := range []string{"blind", "directed"} {
		t := tallyOf(byArm(rs, name))
		fmt.Fprintf(out, "| %s | %d | %d | %d | %d/%d | %d | %d | %s | %s | %s |\n", name, t.runs, t.firstOK, t.notFirst,
			t.recovered, t.notFirst, t.parked, t.failed, avg(t.attempts, t.runs), avg(t.calls, t.runs), avg(t.tokens, t.runs))
	}

	// Per first failure: how many runs started that way, how many ended accepted.
	kinds := map[string]bool{}
	for _, r := range rs {
		if f := r.firstFailure(); f != "" {
			kinds[f] = true
		}
	}
	if len(kinds) > 0 {
		names := make([]string, 0, len(kinds))
		for k := range kinds {
			names = append(names, k)
		}
		sort.Strings(names)
		fmt.Fprintln(out, "\n| first failure | blind: runs, recovered | directed: runs, recovered |")
		fmt.Fprintln(out, "|---|---|---|")
		for _, k := range names {
			cell := func(arm string) string {
				n, ok := 0, 0
				for _, r := range byArm(rs, arm) {
					if r.firstFailure() == k {
						n++
						if r.accepted() {
							ok++
						}
					}
				}
				return fmt.Sprintf("%d, %d", n, ok)
			}
			fmt.Fprintf(out, "| %s | %s | %s |\n", k, cell("blind"), cell("directed"))
		}
	}

	if engine != nil && len(engine.Stats()) > 0 {
		fmt.Fprintln(out, "\n| fix plan | applied | cured its failure | avg attempts when it did |")
		fmt.Fprintln(out, "|---|---|---|---|")
		for _, s := range engine.Stats() {
			fmt.Fprintf(out, "| %s | %d | %d | %s |\n", s.PlanID, s.Total, s.Successes, avgFloat(s.AvgAttempts, s.Successes))
		}
	}
	fmt.Fprintln(out, "\nThe arms draw separate first attempts. With a handful of runs a gap of one or two is noise: read this as a baseline, not a verdict. Runs are in out/fixstats/.")
}

func avgFloat(v float64, n int) string {
	if n == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.1f", v)
}

func save(dir string, i int, r result) {
	if dir == "" {
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	var b strings.Builder
	fmt.Fprintf(&b, "arm: %s\ntopic: %s\nstatus: %s\n\nattempts:\n", r.arm, r.topic, r.status)
	for n, a := range r.attempts {
		fmt.Fprintf(&b, "%d. %s, %d words, %s %s\n", n+1, a.kind, a.words, a.verdict, a.detail)
	}
	fmt.Fprintf(&b, "\nfinal script (what the run holds):\n\n%s\n", r.finalBody)
	_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("%02d-%s.md", i, r.arm)), []byte(b.String()), 0o644)
}

func main() {
	runs := envInt("RUNS", 6, 1, 50)
	duration := envInt("DURATION", 5, 1, 60)
	maxAttempts := envInt("MAX_ATTEMPTS", runner.DefaultMaxAttempts, 1, 10)
	style := os.Getenv("STYLE")
	if style == "" {
		style = "Earthside"
	}
	cfg := script.Config{Style: style, DurationMin: duration, MaxTokensPerChunk: envInt("MAX_TOKENS", 0, 0, 32000)}
	build, label, pause := source()

	if os.Getenv("PAIRED") != "" {
		trials := envInt("TRIALS", 3, 1, 10)
		fmt.Printf("paired test, model: %s, style %s, %d minute episodes, up to %d first attempts, %d trials per arm\npause between calls: %s\n\n", label, style, duration, runs, trials, pause)
		r, err := paired(context.Background(), os.Stdout, build(), cfg, runs, trials, pause, filepath.Join("out", "fixstats"))
		if err != nil {
			fmt.Fprintln(os.Stderr, "failed:", err)
			os.Exit(1)
		}
		pairedReport(os.Stdout, r, label, trials)
		return
	}

	fmt.Printf("model: %s, style %s, %d minute episodes, %d topics, two arms each\npause between runs: %s\n\n", label, style, duration, runs, pause)
	rs, engine, err := experiment(context.Background(), os.Stdout, build, cfg, runs, maxAttempts, pause, filepath.Join("out", "fixstats"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed:", err)
		os.Exit(1)
	}
	report(os.Stdout, rs, engine, label, maxAttempts)
}

// source returns a builder for a fresh provider per arm, a label and the pause
// between runs.
func source() (builder, string, time.Duration) {
	key := os.Getenv("GROQ_API_KEY")
	if key == "" {
		fmt.Fprintln(os.Stderr, "GROQ_API_KEY not set: running a scripted mock that writes a short script unless it is steered. This only proves the harness works, it measures no model.")
		return func() llm.Provider { return shortWriter{} }, "scripted mock", 0
	}
	model := os.Getenv("GROQ_MODEL")
	if model == "" {
		fmt.Fprintln(os.Stderr, "GROQ_MODEL is required when GROQ_API_KEY is set (see https://api.groq.com/openai/v1/models)")
		os.Exit(2)
	}
	effort := os.Getenv("REASONING_EFFORT")
	if err := llm.NewGroq(key, model).SetReasoningEffort(effort); err != nil {
		fmt.Fprintln(os.Stderr, "REASONING_EFFORT:", err)
		os.Exit(2)
	}
	label := "groq:" + model
	if effort != "" {
		label += " [reasoning " + effort + "]"
	}
	build := func() llm.Provider {
		g := llm.NewGroq(key, model)
		_ = g.SetReasoningEffort(effort) // validated above
		return &llm.Retrying{Inner: g, OnRetry: func(attempt int, wait time.Duration, _ error) {
			fmt.Fprintf(os.Stderr, "  rate limited or unavailable, waiting %s (retry %d of 3)\n", wait, attempt)
		}}
	}
	return build, label, time.Duration(envInt("SCRIPT_PAUSE_SECONDS", 15, 0, 600)) * time.Second
}

// shortWriter is the keyless stand-in for a model that under-delivers: its
// scripts are far too short unless the prompt steers it, with a continuation
// or a repair note. A blind retry never changes the prompt, so it never helps.
type shortWriter struct{}

func (shortWriter) Name() string { return "scripted-short-writer" }
func (shortWriter) Complete(_ context.Context, req llm.Request) (llm.Response, error) {
	if strings.Contains(req.Messages[0].Content, "episode name") {
		return llm.Response{Content: "Velvet Meadow"}, nil
	}
	p := req.Messages[1].Content
	if strings.Contains(p, "REPAIR NOTE") || strings.Contains(p, "Continue the script") {
		return llm.Response{Content: script.SampleScript(12)}, nil
	}
	return llm.Response{Content: "The forest is slow and warm. Breathe in. Breathe out."}, nil
}

func envInt(name string, def, min, max int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < min || n > max {
		fmt.Fprintf(os.Stderr, "%s must be a number from %d to %d\n", name, min, max)
		os.Exit(2)
	}
	return n
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
