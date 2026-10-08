package runner_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"sleepy-agent/internal/agent"
	"sleepy-agent/internal/domain"
	"sleepy-agent/internal/errs"
	"sleepy-agent/internal/llm"
	"sleepy-agent/internal/runner"
	"sleepy-agent/internal/store"
)

// rig wires a worker to mocks so tests can assert on what each adapter saw.
type rig struct {
	store     *store.File
	voice     *runner.MockVoice
	renderer  *runner.MockRenderer
	publisher *runner.MockPublisher
	script    *llm.Mock
	worker    *runner.Worker

	mu     sync.Mutex
	events []runner.Event
}

func newRig(t *testing.T, st *store.File, decider agent.Decider) *rig {
	t.Helper()
	r := &rig{
		store:     st,
		voice:     &runner.MockVoice{},
		renderer:  &runner.MockRenderer{},
		publisher: &runner.MockPublisher{},
		script:    llm.NewMock("A Quiet Forest", "The forest is slow and warm. Breathe in. Breathe out."),
	}
	if decider == nil {
		decider = runner.PublishGate{Runs: st}
	}
	w, err := runner.New(st, runner.Steps{
		Scripter:  runner.LLMScripter{Provider: r.script},
		Voice:     r.voice,
		Renderer:  r.renderer,
		Publisher: r.publisher,
	}, decider, runner.Config{WorkerID: "w1", Observer: func(e runner.Event) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.events = append(r.events, e)
	}})
	if err != nil {
		t.Fatal(err)
	}
	r.worker = w
	return r
}

func mustRun(t *testing.T, st *store.File, id string) domain.Run {
	t.Helper()
	run, err := st.GetRun(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestWalkingSkeletonStopsBeforePublishUntilApproved(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	r := newRig(t, st, nil)
	run, _ := st.CreateRun(ctx, "a quiet forest")

	if err := r.worker.Drain(ctx); err != nil {
		t.Fatal(err)
	}

	got := mustRun(t, st, run.ID)
	if got.Status != domain.StatusUploaded || !got.AwaitingApproval {
		t.Fatalf("want UPLOADED and awaiting approval, got %s awaiting=%v", got.Status, got.AwaitingApproval)
	}
	if r.publisher.Calls() != 0 {
		t.Fatalf("publisher was called %d times before approval", r.publisher.Calls())
	}
	if r.renderer.PackageCalls() != 1 {
		t.Fatalf("every step before publish should have run once, package ran %d times", r.renderer.PackageCalls())
	}

	if err := st.ApprovePublish(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.worker.Drain(ctx); err != nil {
		t.Fatal(err)
	}

	got = mustRun(t, st, run.ID)
	if got.Status != domain.StatusDone {
		t.Fatalf("want DONE after approval, got %s", got.Status)
	}
	if reqs := r.publisher.Requests(); len(reqs) != 1 || reqs[0].RunID != run.ID {
		t.Fatalf("want exactly one publish for %s, got %+v", run.ID, reqs)
	}
	if got.Outputs[domain.OutputPublished] != "mock:"+run.ID+" (dry-run)" {
		t.Fatalf("published output = %q", got.Outputs[domain.OutputPublished])
	}
	if got.Outputs[domain.OutputTitle] != "A Quiet Forest" {
		t.Fatalf("title = %q", got.Outputs[domain.OutputTitle])
	}
}

func TestDefaultPublisherPublishesNothing(t *testing.T) {
	res, err := runner.DryRunPublisher{}.Publish(context.Background(), runner.PublishRequest{RunID: "run-1"})
	if err != nil || res.Visibility != "dry-run" {
		t.Fatalf("got %+v, %v", res, err)
	}
}

func TestRunOnceProcessesExactlyOneStep(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	r := newRig(t, st, nil)
	run, _ := st.CreateRun(ctx, "t")

	for i, want := range []domain.RunStatus{domain.StatusScripted, domain.StatusVoiced, domain.StatusThumbnailed} {
		did, err := r.worker.RunOnce(ctx)
		if err != nil || !did {
			t.Fatalf("step %d: did=%v err=%v", i, did, err)
		}
		if got := mustRun(t, st, run.ID).Status; got != want {
			t.Fatalf("after step %d status = %s, want %s", i, got, want)
		}
	}
	if got := mustRun(t, st, run.ID); got.LockedBy != "" {
		t.Fatalf("run still locked by %q after the step", got.LockedBy)
	}
}

// The resume test. "Crash" means: drop the worker, the mocks and the store,
// then open the same file again with brand new objects.
func TestResumeAfterCrashContinuesFromTheStageItWasOn(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runs.json")

	st1, err := store.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	r1 := newRig(t, st1, nil)
	run, _ := st1.CreateRun(ctx, "a quiet forest")
	for i := 0; i < 3; i++ { // script, voice, thumbnail
		if _, err := r1.worker.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := mustRun(t, st1, run.ID).Status; got != domain.StatusThumbnailed {
		t.Fatalf("setup: status = %s", got)
	}

	// Crash. Everything in memory is gone.
	st2, err := store.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	r2 := newRig(t, st2, nil)
	if err := r2.worker.Drain(ctx); err != nil {
		t.Fatal(err)
	}

	if n := len(r2.script.Requests()); n != 0 {
		t.Errorf("script was asked again %d times after the crash", n)
	}
	if r2.voice.Calls() != 0 || r2.renderer.ThumbnailCalls() != 0 {
		t.Errorf("finished steps ran again: voice=%d thumbnail=%d", r2.voice.Calls(), r2.renderer.ThumbnailCalls())
	}
	if r2.renderer.RenderCalls() != 1 {
		t.Errorf("render should run once after the crash, ran %d times", r2.renderer.RenderCalls())
	}
	got := mustRun(t, st2, run.ID)
	if got.Status != domain.StatusUploaded || !got.AwaitingApproval {
		t.Fatalf("want it parked at the approval gate, got %s awaiting=%v", got.Status, got.AwaitingApproval)
	}
	if got.Outputs[domain.OutputScript] == "" || got.Outputs[domain.OutputVoice] == "" {
		t.Errorf("outputs from before the crash were lost: %+v", got.Outputs)
	}
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestLockOfACrashedWorkerExpiresAndTheRunIsReclaimed(t *testing.T) {
	ctx := context.Background()
	clock := &fakeClock{t: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)}
	st := store.NewMemory(clock.Now)
	run, _ := st.CreateRun(ctx, "t")

	if c, err := st.ClaimNextRun(ctx, "crashed-worker"); err != nil || c == nil {
		t.Fatalf("first claim: %v %v", c, err)
	}
	// The worker dies here and never releases.
	if c, _ := st.ClaimNextRun(ctx, "w2"); c != nil {
		t.Fatal("a locked run was claimed again inside the TTL")
	}
	clock.Advance(store.DefaultLockTTL + time.Second)
	c, err := st.ClaimNextRun(ctx, "w2")
	if err != nil || c == nil || c.ID != run.ID || c.LockedBy != "w2" {
		t.Fatalf("expired lock was not reclaimed: %+v %v", c, err)
	}
}

func TestTransientFailureRetriesThenSucceeds(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	r := newRig(t, st, nil)
	r.voice.FailNext(errs.NewTransient("voice", 503, errors.New("overloaded")))
	run, _ := st.CreateRun(ctx, "t")

	if err := r.worker.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	got := mustRun(t, st, run.ID)
	if got.Status != domain.StatusUploaded {
		t.Fatalf("want the run to get past voice and reach UPLOADED, got %s (%s)", got.Status, got.LastError)
	}
	if r.voice.Calls() != 2 {
		t.Fatalf("voice calls = %d, want 2 (one failure, one success)", r.voice.Calls())
	}
	if got.FailedAttempts != 0 {
		t.Fatalf("failure counter should reset when the status changes, got %d", got.FailedAttempts)
	}
}

func TestRepeatedTransientFailureParksTheRunForReview(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	r := newRig(t, st, nil)
	boom := errs.NewTransient("voice", 429, errors.New("rate limited"))
	r.voice.FailNext(boom, boom, boom, boom, boom)
	run, _ := st.CreateRun(ctx, "t")

	if err := r.worker.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	got := mustRun(t, st, run.ID)
	if got.Status != domain.StatusNeedsReview {
		t.Fatalf("want NEEDS_REVIEW, got %s", got.Status)
	}
	if r.voice.Calls() != runner.DefaultMaxAttempts {
		t.Fatalf("voice calls = %d, want %d", r.voice.Calls(), runner.DefaultMaxAttempts)
	}
	if got.LastError == "" {
		t.Fatal("the reason must be recorded for the human who reviews it")
	}
}

func TestPermanentErrorFailsTheRunWithoutRetrying(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	r := newRig(t, st, nil)
	r.renderer.FailNextRender(errors.New("ffmpeg: invalid data"))
	run, _ := st.CreateRun(ctx, "t")

	if err := r.worker.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	got := mustRun(t, st, run.ID)
	if got.Status != domain.StatusFailed {
		t.Fatalf("want FAILED, got %s", got.Status)
	}
	if r.renderer.RenderCalls() != 1 {
		t.Fatalf("a permanent error was retried: %d render calls", r.renderer.RenderCalls())
	}
}

func TestCancelledContextIsNotCountedAsAStepFailure(t *testing.T) {
	st := store.NewMemory(nil)
	r := newRig(t, st, nil)
	run, _ := st.CreateRun(context.Background(), "t")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	did, err := r.worker.RunOnce(ctx)
	if err == nil && did {
		// Claiming with a cancelled context is allowed to succeed in memory; the
		// step itself must then report the cancellation, not a failure.
		t.Log("claimed despite cancelled context")
	}
	got := mustRun(t, st, run.ID)
	if got.FailedAttempts != 0 || got.Status.Terminal() {
		t.Fatalf("cancellation must not fail the run: %+v", got)
	}
	if got.LockedBy != "" {
		t.Fatalf("lock was not released after cancellation: %q", got.LockedBy)
	}
}

func TestDeciderStopParksTheRun(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	stopAtRender := agent.DeciderFunc(func(_ context.Context, c agent.Call) agent.Decision {
		if c.Tool == "stage.render" {
			return agent.Decision{Action: agent.Stop, Reason: "budget exceeded"}
		}
		return agent.Decision{Action: agent.Act}
	})
	r := newRig(t, st, stopAtRender)
	run, _ := st.CreateRun(ctx, "t")

	if err := r.worker.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	got := mustRun(t, st, run.ID)
	if got.Status != domain.StatusNeedsReview {
		t.Fatalf("want NEEDS_REVIEW, got %s", got.Status)
	}
	if r.renderer.RenderCalls() != 0 {
		t.Fatal("render ran although the decider said stop")
	}
}

func TestUnknownDeciderActionFailsClosed(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	weird := agent.DeciderFunc(func(context.Context, agent.Call) agent.Decision {
		return agent.Decision{Action: agent.Action(99)}
	})
	r := newRig(t, st, weird)
	run, _ := st.CreateRun(ctx, "t")

	if err := r.worker.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if got := mustRun(t, st, run.ID); got.Status != domain.StatusNeedsReview {
		t.Fatalf("want NEEDS_REVIEW, got %s", got.Status)
	}
	if len(r.script.Requests()) != 0 {
		t.Fatal("a step ran under an unknown decision")
	}
}

func TestPublishGateFailsClosedWhenTheRunCannotBeRead(t *testing.T) {
	gate := runner.PublishGate{Runs: store.NewMemory(nil)}
	d := gate.Decide(context.Background(), agent.Call{Tool: "stage.publish", Args: map[string]any{"run_id": "nope"}})
	if d.Action != agent.Stop {
		t.Fatalf("want stop, got %s", d.Action)
	}
	if d := gate.Decide(context.Background(), agent.Call{Tool: "stage.voice"}); d.Action != agent.Act {
		t.Fatalf("non publish steps should be allowed, got %s", d.Action)
	}
}

func TestNewRejectsUnsafeConfig(t *testing.T) {
	st := store.NewMemory(nil)
	steps := runner.Steps{
		Scripter:  runner.LLMScripter{Provider: llm.NewMock()},
		Voice:     &runner.MockVoice{},
		Renderer:  &runner.MockRenderer{},
		Publisher: &runner.MockPublisher{},
	}
	if _, err := runner.New(st, steps, nil, runner.Config{}); err == nil {
		t.Error("a nil decider must be rejected, there is no implicit permission")
	}
	if _, err := runner.New(nil, steps, agent.AlwaysAct{}, runner.Config{}); err == nil {
		t.Error("a nil store must be rejected")
	}
	steps.Publisher = nil
	if _, err := runner.New(st, steps, agent.AlwaysAct{}, runner.Config{}); err == nil {
		t.Error("a missing publisher must be rejected")
	}
}

// Several workers on one store: every step of every run must happen exactly
// once. Run with -race.
func TestConcurrentWorkersNeverDoubleProcessARun(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	voice := &runner.MockVoice{}
	renderer := &runner.MockRenderer{}
	publisher := &runner.MockPublisher{}
	const runs, workers = 20, 4

	for i := 0; i < runs; i++ {
		if _, err := st.CreateRun(ctx, fmt.Sprintf("topic %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		w, err := runner.New(st, runner.Steps{
			Scripter:  runner.LLMScripter{Provider: &echoProvider{}},
			Voice:     voice,
			Renderer:  renderer,
			Publisher: publisher,
		}, agent.AlwaysAct{}, runner.Config{WorkerID: fmt.Sprintf("w%d", i)})
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := w.Drain(ctx); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	if voice.Calls() != runs || renderer.RenderCalls() != runs || publisher.Calls() != runs {
		t.Fatalf("each step should run once per run: voice=%d render=%d publish=%d, want %d",
			voice.Calls(), renderer.RenderCalls(), publisher.Calls(), runs)
	}
	for i := 1; i <= runs; i++ {
		if got := mustRun(t, st, fmt.Sprintf("run-%d", i)); got.Status != domain.StatusDone {
			t.Errorf("run-%d ended in %s", i, got.Status)
		}
	}
}

// echoProvider answers any request, so concurrent runs never exhaust a script.
type echoProvider struct{}

func (*echoProvider) Name() string { return "echo" }
func (*echoProvider) Complete(context.Context, llm.Request) (llm.Response, error) {
	return llm.Response{Content: "calm words"}, nil
}

func TestObserverSeesTheApprovalQuestion(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	r := newRig(t, st, nil)
	st.CreateRun(ctx, "t")
	if err := r.worker.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	last := r.events[len(r.events)-1]
	if last.Stage != "publish" || last.Decision != agent.Ask {
		t.Fatalf("last event = %+v, want an Ask on publish", last)
	}
	if len(r.events) != 7 { // script, voice, thumbnail, render, package, check, then the Ask
		t.Fatalf("got %d events, want 7", len(r.events))
	}
}

// forgetfulStore never remembers that a run is waiting for approval, so the
// same run is handed back again and again. Drain must notice.
type forgetfulStore struct{ *store.File }

func (forgetfulStore) SetAwaitingApproval(context.Context, string, bool) error { return nil }

func TestDrainStopsWhenTheStoreKeepsHandingBackTheSameRun(t *testing.T) {
	ctx := context.Background()
	st := forgetfulStore{store.NewMemory(nil)}
	steps := runner.Steps{
		Scripter:  runner.LLMScripter{Provider: &echoProvider{}},
		Voice:     &runner.MockVoice{},
		Renderer:  &runner.MockRenderer{},
		Publisher: &runner.MockPublisher{},
	}
	w, err := runner.New(st, steps, runner.PublishGate{Runs: st}, runner.Config{MaxDrainSteps: 50})
	if err != nil {
		t.Fatal(err)
	}
	st.CreateRun(ctx, "t")
	if err := w.Drain(ctx); err == nil {
		t.Fatal("Drain should return an error instead of looping forever")
	}
}
