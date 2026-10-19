package runner_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"sleepy-agent/internal/agent"
	"sleepy-agent/internal/domain"
	"sleepy-agent/internal/errs"
	"sleepy-agent/internal/fix"
	"sleepy-agent/internal/llm"
	"sleepy-agent/internal/policy"
	"sleepy-agent/internal/runner"
	"sleepy-agent/internal/script"
	"sleepy-agent/internal/store"
)

// events collects what a worker reports.
type events struct {
	mu   sync.Mutex
	list []runner.Event
}

func (e *events) observe(ev runner.Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.list = append(e.list, ev)
}

// reasons returns the reason of every event, in order.
func (e *events) reasons() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, ev := range e.list {
		out = append(out, ev.Reason)
	}
	return out
}

func (e *events) has(substr string) bool {
	for _, r := range e.reasons() {
		if strings.Contains(r, substr) {
			return true
		}
	}
	return false
}

// build wires a worker around a scripter, with the voice, render and publish
// mocks. The caller's config gets the observer.
func build(t *testing.T, st *store.File, sc runner.Scripter, cfg runner.Config) (*runner.Worker, *events) {
	t.Helper()
	ev := &events{}
	cfg.Observer = ev.observe
	w, err := runner.New(st, runner.Steps{
		Scripter: sc, Voice: &runner.MockVoice{}, Renderer: &runner.MockRenderer{}, Publisher: &runner.MockPublisher{},
	}, agent.AlwaysAct{}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return w, ev
}

// A model that answers from a script: each script call is followed by a title
// call, except a continuation or a retitle, which the tests spell out.
func llmScripter(replies ...string) (runner.LLMScripter, *llm.Mock) {
	m := llm.NewMock(replies...)
	return runner.LLMScripter{Provider: m}, m
}

func okScript() string { return script.SampleScript(12) }

func bannedScript() string { return strings.Replace(script.SampleScript(12), "river", "disappear", 2) }

func drain(t *testing.T, w *runner.Worker) {
	t.Helper()
	if err := w.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func statOf(e *fix.Engine, id string) fix.Stat {
	for _, s := range e.Stats() {
		if s.PlanID == id {
			return s
		}
	}
	return fix.Stat{PlanID: id}
}

func TestAShortScriptIsContinuedNotRewritten(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	engine := fix.NewEngine()
	// attempt 1: a short script and its title; attempt 2: only the continuation, the title is kept.
	sc, m := llmScripter(shortScript, "Soft Lake", okScript())
	w, ev := build(t, st, sc, runner.Config{Fixes: engine})
	run, _ := st.CreateRun(ctx, "a soft lake")

	drain(t, w)

	got := mustRun(t, st, run.ID)
	if got.Status != domain.StatusDone {
		t.Fatalf("want DONE, got %s (%s)", got.Status, got.LastError)
	}
	if !strings.HasPrefix(got.Outputs[domain.OutputScript], shortScript+"\n\n") || !strings.HasSuffix(got.Outputs[domain.OutputScript], okScript()) {
		t.Fatalf("the first script must stay and the continuation follow it: %q", got.Outputs[domain.OutputScript])
	}
	if got.Outputs[domain.OutputTitle] != "Soft Lake" {
		t.Fatalf("title = %q", got.Outputs[domain.OutputTitle])
	}
	reqs := m.Requests()
	if len(reqs) != 3 {
		t.Fatalf("model calls = %d, want 3 (script, title, continuation)", len(reqs))
	}
	cont := reqs[2].Messages[1].Content
	for _, want := range []string{"Continue the script", "The previous script had 10 words", "minimum is 500", "Breathe out."} {
		if !strings.Contains(cont, want) {
			t.Errorf("the continuation prompt should contain %q:\n%s", want, cont)
		}
	}
	if got.Outputs[domain.OutputFixState] != "" || got.FailedAttempts != 0 {
		t.Fatalf("fix state %q and failures %d must be clear once the gate passes", got.Outputs[domain.OutputFixState], got.FailedAttempts)
	}
	if !ev.has("will retry with fix wc_low_continue") || !ev.has("qa passed after fix wc_low_continue") {
		t.Fatalf("events: %q", ev.reasons())
	}
	if s := statOf(engine, "wc_low_continue"); s.Total != 1 || s.Successes != 1 {
		t.Fatalf("the plan should be credited once: %+v", s)
	}
}

func TestAPlanThatDoesNotWorkIsFollowedByTheNextOneAndThenTheRunIsParked(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	engine := fix.NewEngine()
	tiny := "More slow words appear here tonight."
	sc, _ := llmScripter(shortScript, "Soft Lake", tiny, tiny)
	w, ev := build(t, st, sc, runner.Config{Fixes: engine})
	run, _ := st.CreateRun(ctx, "t")

	drain(t, w)

	got := mustRun(t, st, run.ID)
	if got.Status != domain.StatusNeedsReview {
		t.Fatalf("want NEEDS_REVIEW, got %s", got.Status)
	}
	for _, want := range []string{"3 times", "fixes tried: wc_low_continue, wc_low_continue_expand"} {
		if !strings.Contains(got.LastError, want) {
			t.Errorf("reason %q should contain %q", got.LastError, want)
		}
	}
	if !ev.has("will retry with fix wc_low_continue_expand") {
		t.Fatalf("events: %q", ev.reasons())
	}
	// Both plans failed to cure the length, and the scorer was told so.
	for _, id := range []string{"wc_low_continue", "wc_low_continue_expand"} {
		if s := statOf(engine, id); s.Total != 1 || s.Successes != 0 {
			t.Errorf("%s: %+v", id, s)
		}
	}
	// What the reviewer sees is the script as far as the fixes got.
	if !strings.Contains(got.Outputs[domain.OutputScript], shortScript) || !strings.Contains(got.Outputs[domain.OutputScript], tiny) {
		t.Errorf("script %q", got.Outputs[domain.OutputScript])
	}
}

func TestASecondPlanThatWorksIsCredited(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	engine := fix.NewEngine()
	sc, _ := llmScripter(shortScript, "Soft Lake", "Still short here tonight.", okScript())
	w, _ := build(t, st, sc, runner.Config{Fixes: engine})
	run, _ := st.CreateRun(ctx, "t")

	drain(t, w)

	if got := mustRun(t, st, run.ID); got.Status != domain.StatusDone {
		t.Fatalf("want DONE, got %s (%s)", got.Status, got.LastError)
	}
	if s := statOf(engine, "wc_low_continue"); s.Successes != 0 || s.Total != 1 {
		t.Errorf("first plan: %+v", s)
	}
	if s := statOf(engine, "wc_low_continue_expand"); s.Successes != 1 || s.AvgAttempts != 3 {
		t.Errorf("second plan should have passed on attempt 3: %+v", s)
	}
}

func TestAPlanIsScoredOnItsOwnFailureNotOnTheNextOne(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	engine := fix.NewEngine()
	// The continuation cures the length but the extra text brings a banned word.
	withBanned := strings.Replace(okScript(), "river", "disappear", 2)
	sc, _ := llmScripter(shortScript, "Soft Lake", withBanned, okScript(), "Soft Lake")
	w, ev := build(t, st, sc, runner.Config{Fixes: engine})
	run, _ := st.CreateRun(ctx, "t")

	drain(t, w)

	if got := mustRun(t, st, run.ID); got.Status != domain.StatusDone {
		t.Fatalf("want DONE, got %s (%s)", got.Status, got.LastError)
	}
	if s := statOf(engine, "wc_low_continue"); s.Successes != 1 {
		t.Errorf("the continuation fixed the length, it must be credited even though the run was rejected again: %+v", s)
	}
	if !ev.has("will retry with fix banned_conservative") {
		t.Fatalf("a banned word after the length is right must get the banned word plan: %q", ev.reasons())
	}
}

func TestABannedWordPlanNamesTheWordsAndRunsColder(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	sc, m := llmScripter(bannedScript(), "Soft Lake", okScript(), "Soft Lake")
	w, _ := build(t, st, sc, runner.Config{Fixes: fix.NewEngine()})
	run, _ := st.CreateRun(ctx, "t")

	drain(t, w)

	if got := mustRun(t, st, run.ID); got.Status != domain.StatusDone {
		t.Fatalf("want DONE, got %s (%s)", got.Status, got.LastError)
	}
	repair := m.Requests()[2]
	if repair.Temperature != 0.3 {
		t.Errorf("temperature = %v, want 0.3", repair.Temperature)
	}
	if p := repair.Messages[1].Content; !strings.Contains(p, "REPAIR NOTE") || !strings.Contains(p, `"disappear"`) {
		t.Errorf("the note must name the banned word:\n%s", p)
	}
	if first := m.Requests()[0]; first.Temperature != 0.7 || strings.Contains(first.Messages[1].Content, "REPAIR NOTE") {
		t.Error("the first attempt must be an ordinary one")
	}
}

func TestABadTitleAsksForAnotherTitleAndKeepsTheScript(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	sc, m := llmScripter(okScript(), "VELVET MEADOW", "Amber Lanterns")
	w, _ := build(t, st, sc, runner.Config{Fixes: fix.NewEngine()})
	run, _ := st.CreateRun(ctx, "t")

	drain(t, w)

	got := mustRun(t, st, run.ID)
	if got.Status != domain.StatusDone || got.Outputs[domain.OutputTitle] != "Amber Lanterns" {
		t.Fatalf("%s %q (%s)", got.Status, got.Outputs[domain.OutputTitle], got.LastError)
	}
	if got.Outputs[domain.OutputScript] != okScript() {
		t.Fatal("the script was fine and must be kept")
	}
	if n := len(m.Requests()); n != 3 {
		t.Fatalf("model calls = %d, want 3 (script, bad title, new title)", n)
	}
	if sys := m.Requests()[2].Messages[0].Content; !strings.Contains(sys, "title is ALL CAPS") {
		t.Errorf("the title prompt should say why the last one was rejected:\n%s", sys)
	}
}

func TestWithoutAFixEngineARejectionRepeatsTheSameRequest(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	sc, m := llmScripter(shortScript, "Soft Lake", okScript(), "Soft Lake")
	w, ev := build(t, st, sc, runner.Config{})
	run, _ := st.CreateRun(ctx, "t")

	drain(t, w)

	if got := mustRun(t, st, run.ID); got.Status != domain.StatusDone {
		t.Fatalf("want DONE, got %s", got.Status)
	}
	reqs := m.Requests()
	if reqs[0].Messages[1].Content != reqs[2].Messages[1].Content || reqs[0].Temperature != reqs[2].Temperature {
		t.Fatal("the retry without an engine must be the very same request")
	}
	if ev.has("fix") {
		t.Fatalf("no fix without an engine: %q", ev.reasons())
	}
}

// plainScripter is a Scripter that cannot be steered.
type plainScripter struct {
	mu      sync.Mutex
	replies []runner.Script
	calls   int
}

func (p *plainScripter) Script(context.Context, string) (runner.Script, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.calls >= len(p.replies) {
		return runner.Script{}, errors.New("plain scripter: out of replies")
	}
	p.calls++
	return p.replies[p.calls-1], nil
}

func TestAScripterThatCannotBeSteeredStillGetsPlainRetries(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	p := &plainScripter{replies: []runner.Script{{Title: "Soft Lake", Body: shortScript}, {Title: "Soft Lake", Body: okScript()}}}
	w, _ := build(t, st, p, runner.Config{Fixes: fix.NewEngine()})
	run, _ := st.CreateRun(ctx, "t")

	drain(t, w)

	if got := mustRun(t, st, run.ID); got.Status != domain.StatusDone || p.calls != 2 {
		t.Fatalf("%s after %d calls (%s)", got.Status, p.calls, got.LastError)
	}
}

func TestATransientErrorDuringAFixKeepsThePlan(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	engine := fix.NewEngine()
	sc, m := llmScripter(shortScript, "Soft Lake", okScript())
	w, _ := build(t, st, sc, runner.Config{Fixes: engine})
	run, _ := st.CreateRun(ctx, "t")

	if _, err := w.RunOnce(ctx); err != nil { // attempt 1 is rejected, a fix is chosen
		t.Fatal(err)
	}
	if got := mustRun(t, st, run.ID); !strings.Contains(got.Outputs[domain.OutputFixState], "wc_low_continue") {
		t.Fatalf("a fix should be chosen after the rejection, state %q (%s)", got.Outputs[domain.OutputFixState], got.LastError)
	}
	m.FailNext(errs.NewTransient("mock", 503, errors.New("overloaded")))
	if _, err := w.RunOnce(ctx); err != nil { // the fix attempt hits a transient error
		t.Fatal(err)
	}
	got := mustRun(t, st, run.ID)
	if !strings.Contains(got.Outputs[domain.OutputFixState], `"tried":["wc_low_continue"]`) {
		t.Fatalf("the transient error must not use up the plan or change it: %q", got.Outputs[domain.OutputFixState])
	}
	if s := statOf(engine, "wc_low_continue"); s.Total != 0 {
		t.Fatalf("an attempt that never got a verdict is no outcome: %+v", s)
	}

	drain(t, w)

	if got := mustRun(t, st, run.ID); got.Status != domain.StatusDone {
		t.Fatalf("want DONE, got %s (%s)", got.Status, got.LastError)
	}
	if s := statOf(engine, "wc_low_continue"); s.Total != 1 || s.Successes != 1 {
		t.Fatalf("%+v", s)
	}
}

func TestFixStateSurvivesACrashBetweenAttempts(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runs.json")
	st, err := store.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	sc, _ := llmScripter(shortScript, "Soft Lake")
	w, _ := build(t, st, sc, runner.Config{Fixes: fix.NewEngine()})
	run, _ := st.CreateRun(ctx, "t")
	if _, err := w.RunOnce(ctx); err != nil { // attempt 1 is rejected, then the process dies
		t.Fatal(err)
	}

	// A new process opens the same file, with a new engine and a model that only
	// knows the continuation.
	st2, err := store.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	sc2, m2 := llmScripter(okScript())
	w2, _ := build(t, st2, sc2, runner.Config{Fixes: fix.NewEngine()})
	drain(t, w2)

	got := mustRun(t, st2, run.ID)
	if got.Status != domain.StatusDone {
		t.Fatalf("want DONE, got %s (%s)", got.Status, got.LastError)
	}
	if n := len(m2.Requests()); n != 1 || !strings.Contains(m2.Requests()[0].Messages[1].Content, "Continue the script") {
		t.Fatalf("the successor must continue the script, not start over: %d calls", n)
	}
}

func TestTruncationsStayPlainRetriesAndKeepTheEngineOutOfIt(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	engine := fix.NewEngine()
	cut := llm.Response{Content: "The scene settles into gentle harmony", FinishReason: "length"}
	m := llm.NewMockResponses(cut, llm.Response{Content: okScript()}, llm.Response{Content: "Soft Lake"})
	w, ev := build(t, st, runner.LLMScripter{Provider: m}, runner.Config{Fixes: engine})
	run, _ := st.CreateRun(ctx, "t")

	drain(t, w)

	if got := mustRun(t, st, run.ID); got.Status != domain.StatusDone {
		t.Fatalf("want DONE, got %s (%s)", got.Status, got.LastError)
	}
	if ev.has("fix ") {
		t.Fatalf("a truncation has no plan yet: %q", ev.reasons())
	}
	if len(engine.Stats()) != 0 {
		t.Fatalf("nothing to score: %+v", engine.Stats())
	}
}

// ---- idempotency

func TestTheSameTopicTwiceMakesNoSecondModelCall(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	cache := runner.NewMemoryCache()
	sc, m := llmScripter(okScript(), "Soft Lake")
	w, ev := build(t, st, sc, runner.Config{Cache: cache})
	first, _ := st.CreateRun(ctx, "a soft lake")
	drain(t, w)
	if n := len(m.Requests()); n != 2 {
		t.Fatalf("the first run costs a script and a title call, got %d", n)
	}

	second, _ := st.CreateRun(ctx, "a soft lake")
	drain(t, w)

	if n := len(m.Requests()); n != 2 {
		t.Fatalf("the second run with the same input must make 0 model calls, total is now %d", n)
	}
	a, b := mustRun(t, st, first.ID), mustRun(t, st, second.ID)
	if b.Status != domain.StatusDone || b.Outputs[domain.OutputScript] != a.Outputs[domain.OutputScript] || b.Outputs[domain.OutputTitle] != "Soft Lake" {
		t.Fatalf("second run %s, script equal %v", b.Status, b.Outputs[domain.OutputScript] == a.Outputs[domain.OutputScript])
	}
	if a.Outputs[domain.OutputScriptSource] != domain.SourceModel || b.Outputs[domain.OutputScriptSource] != domain.SourceCache {
		t.Fatalf("sources: %q then %q", a.Outputs[domain.OutputScriptSource], b.Outputs[domain.OutputScriptSource])
	}
	if !strings.Contains(b.Outputs[domain.OutputQAScript], `"pass":true`) {
		t.Fatal("a cached script goes through the gate like any other")
	}
	if !ev.has("script served from cache, no model call") {
		t.Fatalf("events: %q", ev.reasons())
	}
}

func TestAnotherTopicIsNotServedFromTheCache(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	sc, m := llmScripter(okScript(), "Soft Lake", okScript(), "Slow Moons")
	w, _ := build(t, st, sc, runner.Config{Cache: runner.NewMemoryCache()})
	st.CreateRun(ctx, "a soft lake") //nolint:errcheck // the test reads the run back by status
	st.CreateRun(ctx, "slow moons")  //nolint:errcheck // same
	drain(t, w)
	if n := len(m.Requests()); n != 4 {
		t.Fatalf("two topics are two model runs, got %d calls", n)
	}
}

func TestARejectedScriptIsNeverCached(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	cache := runner.NewMemoryCache()
	sc, _ := llmScripter(shortScript, "A", shortScript, "B", shortScript, "C")
	w, _ := build(t, st, sc, runner.Config{Cache: cache})
	run, _ := st.CreateRun(ctx, "t")
	drain(t, w)
	if got := mustRun(t, st, run.ID); got.Status != domain.StatusNeedsReview {
		t.Fatalf("want NEEDS_REVIEW, got %s", got.Status)
	}
	if _, hit, _ := cache.Get(runner.CacheKey(sc.Fingerprint(), "t")); hit {
		t.Fatal("a script the gate rejected must never be served again")
	}
}

func TestACachedScriptThatTheGateNowRejectsIsEvictedAndWrittenAgain(t *testing.T) {
	ctx := context.Background()
	cache := runner.NewMemoryCache()

	st1 := store.NewMemory(nil)
	sc, _ := llmScripter(okScript(), "Soft Lake") // 624 words, fine for 5 minutes
	w1, _ := build(t, st1, sc, runner.Config{Cache: cache})
	st1.CreateRun(ctx, "t") //nolint:errcheck // not read back
	drain(t, w1)

	// The same cache, but the gate now holds a 1 minute policy: 100 to 160 words.
	st2 := store.NewMemory(nil)
	sc2 := runner.LLMScripter{Provider: llm.NewMock(script.SampleScript(3), "Soft Lake")}
	sc2.Config = script.Config{} // same fingerprint inputs as sc: the provider name is "mock" for both
	w2, ev := build(t, st2, sc2, runner.Config{Cache: cache, Policy: policy.ForDuration(1)})
	run, _ := st2.CreateRun(ctx, "t")
	drain(t, w2)

	got := mustRun(t, st2, run.ID)
	if got.Status != domain.StatusDone || got.Outputs[domain.OutputScript] != script.SampleScript(3) {
		t.Fatalf("want the model's new script to be accepted, got %s (%s)", got.Status, got.LastError)
	}
	if !ev.has("qa rejected 1 of 3") {
		t.Fatalf("the stale cached script should have been rejected once: %q", ev.reasons())
	}
	cached, hit, _ := cache.Get(runner.CacheKey(sc.Fingerprint(), "t"))
	if !hit || cached.Body != script.SampleScript(3) {
		t.Fatal("the cache should now hold the script the gate accepted")
	}
}

func TestAScripterWithoutAFingerprintIsNotCached(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	p := &plainScripter{replies: []runner.Script{{Title: "Soft Lake", Body: okScript()}, {Title: "Soft Lake", Body: okScript()}}}
	cache := runner.NewMemoryCache()
	w, _ := build(t, st, p, runner.Config{Cache: cache})
	st.CreateRun(ctx, "t") //nolint:errcheck // not read back
	st.CreateRun(ctx, "t") //nolint:errcheck // not read back
	drain(t, w)
	if p.calls != 2 {
		t.Fatalf("without a fingerprint nothing is safe to reuse, calls = %d", p.calls)
	}
}

func TestACacheIsSharedAcrossProcessesThroughAFile(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cache.json")

	c1, err := runner.OpenFileCache(path)
	if err != nil {
		t.Fatal(err)
	}
	sc, _ := llmScripter(okScript(), "Soft Lake")
	st1 := store.NewMemory(nil)
	w1, _ := build(t, st1, sc, runner.Config{Cache: c1})
	st1.CreateRun(ctx, "a soft lake") //nolint:errcheck // not read back
	drain(t, w1)

	c2, err := runner.OpenFileCache(path)
	if err != nil {
		t.Fatal(err)
	}
	sc2, m2 := llmScripter() // a model with nothing to say: any call would fail
	st2 := store.NewMemory(nil)
	w2, _ := build(t, st2, sc2, runner.Config{Cache: c2})
	run, _ := st2.CreateRun(ctx, "a soft lake")
	drain(t, w2)

	if got := mustRun(t, st2, run.ID); got.Status != domain.StatusDone || got.Outputs[domain.OutputScript] != okScript() {
		t.Fatalf("the second process should have the script from the file: %s (%s)", got.Status, got.LastError)
	}
	if n := len(m2.Requests()); n != 0 {
		t.Fatalf("model calls in the second process = %d, want 0", n)
	}
}

func TestFileCacheRoundTripAndErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "cache.json")

	c, err := runner.OpenFileCache(path)
	if err != nil {
		t.Fatalf("a missing file is an empty cache: %v", err)
	}
	if _, hit, _ := c.Get("k"); hit {
		t.Fatal("empty cache hit")
	}
	if err := c.Put("k", runner.Script{Title: "T", Body: "B\n\nC"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Put("gone", runner.Script{Title: "x", Body: "y"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete("gone"); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete("never there"); err != nil {
		t.Fatalf("deleting a missing key is not an error: %v", err)
	}

	back, err := runner.OpenFileCache(path)
	if err != nil {
		t.Fatal(err)
	}
	if s, hit, _ := back.Get("k"); !hit || s.Title != "T" || s.Body != "B\n\nC" {
		t.Fatalf("round trip lost the script: %+v %v", s, hit)
	}
	if _, hit, _ := back.Get("gone"); hit {
		t.Fatal("a deleted entry must stay deleted")
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("no temp files may be left behind, found %d entries", len(entries))
	}

	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.OpenFileCache(bad); err == nil || !strings.Contains(err.Error(), "not a cache file") {
		t.Fatalf("a file that is not a cache must be an error, never silently replaced: %v", err)
	}
	if b, _ := os.ReadFile(bad); string(b) != "not json" {
		t.Fatal("the bad file must be left alone")
	}
}

func TestCacheKey(t *testing.T) {
	base := runner.CacheKey("fp", "a soft lake")
	if base != runner.CacheKey("fp", "  a   soft\tlake \n") {
		t.Error("whitespace must not change the key")
	}
	for name, other := range map[string]string{
		"case":        runner.CacheKey("fp", "A soft lake"),
		"topic":       runner.CacheKey("fp", "a soft pond"),
		"fingerprint": runner.CacheKey("fp2", "a soft lake"),
	} {
		if other == base {
			t.Errorf("a different %s must give a different key", name)
		}
	}
	if runner.CacheKey("ab", "c") == runner.CacheKey("a", "bc") {
		t.Error("the boundary between fingerprint and topic must count")
	}
}

func TestConcurrentWorkersShareOneCacheSafely(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	cache := runner.NewMemoryCache()
	var workers []*runner.Worker
	for i := 0; i < 4; i++ {
		w, _ := build(t, st, runner.LLMScripter{Provider: &echoProvider{}}, runner.Config{Cache: cache, WorkerID: fmt.Sprintf("w%d", i)})
		workers = append(workers, w)
	}
	for i := 0; i < 8; i++ {
		st.CreateRun(ctx, "same topic") //nolint:errcheck // checked through the run statuses below
	}
	var wg sync.WaitGroup
	for _, w := range workers {
		wg.Add(1)
		go func(w *runner.Worker) {
			defer wg.Done()
			if err := w.Drain(ctx); err != nil {
				t.Error(err)
			}
		}(w)
	}
	wg.Wait()
	for i := 1; i <= 8; i++ {
		if got := mustRun(t, st, fmt.Sprintf("run-%d", i)); got.Status != domain.StatusDone {
			t.Errorf("run-%d ended in %s (%s)", i, got.Status, got.LastError)
		}
	}
}

func TestWhenNoPlanIsLeftTheOldPlanIsDroppedAndTheRetryIsPlain(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	// Two bad titles in a row: the only title plan has been tried, so the third
	// attempt is a plain retry, and it must not carry the old plan along.
	sc, m := llmScripter(okScript(), "VELVET MEADOW", "STILL SHOUTING", okScript(), "Amber Lanterns")
	w, ev := build(t, st, sc, runner.Config{Fixes: fix.NewEngine(), MaxAttempts: 4})
	run, _ := st.CreateRun(ctx, "t")

	drain(t, w)

	got := mustRun(t, st, run.ID)
	if got.Status != domain.StatusDone {
		t.Fatalf("%s (%s)", got.Status, got.LastError)
	}
	if left := got.Outputs[domain.OutputFixState]; left != "" {
		t.Errorf("fix state left behind: %q", left)
	}
	if ev.has("qa passed after fix") {
		t.Errorf("a plain retry passed, no fix deserves the credit: %q", ev.reasons())
	}
	last := m.Requests()[3].Messages[1].Content
	if strings.Contains(last, "REPAIR NOTE") {
		t.Errorf("the plain retry must not carry a repair note:\n%s", last)
	}
}
