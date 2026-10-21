package runner_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"sleepy-agent/internal/domain"
	"sleepy-agent/internal/judge"
	"sleepy-agent/internal/runner"
	"sleepy-agent/internal/store"
)

// fakeReviewer records what it was asked and answers as scripted.
type fakeReviewer struct {
	mu      sync.Mutex
	asked   []string // the bodies it saw
	topics  []string
	verdict judge.Verdict
	err     error
	block   bool
}

func (f *fakeReviewer) Review(ctx context.Context, topic, title, body string) (judge.Verdict, error) {
	f.mu.Lock()
	f.asked = append(f.asked, body)
	f.topics = append(f.topics, topic)
	f.mu.Unlock()
	if f.block {
		<-ctx.Done()
		return judge.Verdict{}, ctx.Err()
	}
	return f.verdict, f.err
}

func (f *fakeReviewer) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.asked)
}

func TestTheShadowVerdictIsRecordedAndTheRunGoesOn(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	rv := &fakeReviewer{verdict: judge.Verdict{Calm: 5, Coherence: 4, Variety: 4, Pass: true, MinScore: 3}}
	sc, _ := llmScripter(okScript(), "Soft Lake")
	w, ev := build(t, st, sc, runner.Config{Shadow: rv})
	run, _ := st.CreateRun(ctx, "a soft lake")

	drain(t, w)

	got := mustRun(t, st, run.ID)
	if got.Status != domain.StatusDone {
		t.Fatalf("status %s (%s)", got.Status, got.LastError)
	}
	var v judge.Verdict
	if err := json.Unmarshal([]byte(got.Outputs[domain.OutputShadowVerdict]), &v); err != nil || !v.Pass || v.Coherence != 4 {
		t.Fatalf("recorded %q: %v", got.Outputs[domain.OutputShadowVerdict], err)
	}
	if rv.topics[0] != "a soft lake" || rv.asked[0] != okScript() {
		t.Errorf("the reviewer saw topic %q and a different script", rv.topics[0])
	}
	if !ev.has("shadow review: pass (calm 5, coherence 4, variety 4)") {
		t.Fatalf("events: %q", ev.reasons())
	}
}

func TestAShadowRejectionChangesNothing(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	rv := &fakeReviewer{verdict: judge.Verdict{Calm: 5, Coherence: 1, Variety: 2, Pass: false, MinScore: 3, Reason: "forest images in a library"}}
	sc, _ := llmScripter(okScript(), "Soft Lake")
	w, ev := build(t, st, sc, runner.Config{Shadow: rv})
	run, _ := st.CreateRun(ctx, "t")

	drain(t, w)

	got := mustRun(t, st, run.ID)
	if got.Status != domain.StatusDone || got.FailedAttempts != 0 {
		t.Fatalf("a reviewer that would reject must not stop the run: %s, %d failed attempts", got.Status, got.FailedAttempts)
	}
	if !strings.Contains(got.Outputs[domain.OutputShadowVerdict], `"pass":false`) {
		t.Errorf("recorded %q", got.Outputs[domain.OutputShadowVerdict])
	}
	if !ev.has("shadow review: would reject (calm 5, coherence 1, variety 2): forest images in a library") {
		t.Fatalf("events: %q", ev.reasons())
	}
}

func TestAFailingReviewerDoesNotFailTheRun(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	rv := &fakeReviewer{err: errors.New("provider down")}
	sc, _ := llmScripter(okScript(), "Soft Lake")
	w, ev := build(t, st, sc, runner.Config{Shadow: rv})
	run, _ := st.CreateRun(ctx, "t")

	drain(t, w)

	got := mustRun(t, st, run.ID)
	if got.Status != domain.StatusDone {
		t.Fatalf("status %s (%s)", got.Status, got.LastError)
	}
	if _, ok := got.Outputs[domain.OutputShadowVerdict]; ok {
		t.Error("no verdict should be recorded when the review failed")
	}
	if !ev.has("shadow review failed: provider down") {
		t.Fatalf("events: %q", ev.reasons())
	}
}

func TestAReviewerThatHangsIsCutOff(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	rv := &fakeReviewer{block: true}
	sc, _ := llmScripter(okScript(), "Soft Lake")
	w, ev := build(t, st, sc, runner.Config{Shadow: rv, ShadowTimeout: 30 * time.Millisecond})
	run, _ := st.CreateRun(ctx, "t")

	drain(t, w)

	if got := mustRun(t, st, run.ID); got.Status != domain.StatusDone {
		t.Fatalf("status %s (%s)", got.Status, got.LastError)
	}
	if !ev.has("shadow review failed") {
		t.Fatalf("events: %q", ev.reasons())
	}
}

func TestOnlyTheAcceptedScriptIsReviewed(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	rv := &fakeReviewer{verdict: judge.Verdict{Calm: 4, Coherence: 4, Variety: 4, Pass: true, MinScore: 3}}
	sc, _ := llmScripter(shortScript, "Soft Lake", okScript(), "Soft Lake")
	w, _ := build(t, st, sc, runner.Config{Shadow: rv})
	run, _ := st.CreateRun(ctx, "t")

	drain(t, w)

	if got := mustRun(t, st, run.ID); got.Status != domain.StatusDone {
		t.Fatalf("status %s (%s)", got.Status, got.LastError)
	}
	if rv.calls() != 1 || rv.asked[0] != okScript() {
		t.Fatalf("the reviewer saw %d scripts, want only the accepted one", rv.calls())
	}
}

func TestACachedScriptIsNotReviewedAgain(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	rv := &fakeReviewer{verdict: judge.Verdict{Calm: 4, Coherence: 4, Variety: 4, Pass: true, MinScore: 3}}
	sc, _ := llmScripter(okScript(), "Soft Lake")
	w, ev := build(t, st, sc, runner.Config{Shadow: rv, Cache: runner.NewMemoryCache()})
	st.CreateRun(ctx, "same topic")
	drain(t, w)
	second, _ := st.CreateRun(ctx, "same topic")
	drain(t, w)

	if rv.calls() != 1 {
		t.Fatalf("reviewer calls = %d, want 1: the second script came from the cache", rv.calls())
	}
	if _, ok := mustRun(t, st, second.ID).Outputs[domain.OutputShadowVerdict]; ok {
		t.Error("a cached script has no new verdict")
	}
	if !ev.has("script served from cache, no model call") {
		t.Fatalf("events: %q", ev.reasons())
	}
}

func TestNoReviewerNoVerdict(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	sc, _ := llmScripter(okScript(), "Soft Lake")
	w, _ := build(t, st, sc, runner.Config{})
	run, _ := st.CreateRun(ctx, "t")
	drain(t, w)
	if _, ok := mustRun(t, st, run.ID).Outputs[domain.OutputShadowVerdict]; ok {
		t.Error("a verdict appeared without a reviewer")
	}
}
