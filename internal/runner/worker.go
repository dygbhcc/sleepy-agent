// Package runner drives runs through the pipeline state machine.
//
// It is the Sleepy worker (internal/jobs/worker.go) reduced to its skeleton:
// claim a run, process exactly ONE step, release it, loop. Because the status
// is the only memory, a run that dies in the middle resumes from the step it
// was on. What Sleepy bolts onto this loop arrives later: the fix engine on
// Day 7 and 8, guardrails on Day 9. Since Day 6 a step's output goes through a
// QA gate before the run may advance.
//
// New in this repo: before every step the worker asks an agent.Decider whether
// it may go ahead. That is where the human approval gate lives.
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"sleepy-agent/internal/agent"
	"sleepy-agent/internal/domain"
	"sleepy-agent/internal/errs"
	"sleepy-agent/internal/policy"
	"sleepy-agent/internal/qa"
	"sleepy-agent/internal/script"
)

const toolPrefix = "stage."

// DefaultMaxAttempts is how many times in a row a step may fail with a
// transient error before the run is parked for a human.
const DefaultMaxAttempts = 3

// DefaultMaxDrainSteps bounds one Drain call. A healthy store never gets near
// it. It exists so a store bug that keeps handing back the same run ends in an
// error instead of an endless loop.
const DefaultMaxDrainSteps = 10_000

// Store is what the worker needs from run storage. The method names follow
// Sleepy's db package so porting to a database later is mechanical.
type Store interface {
	ClaimNextRun(ctx context.Context, workerID string) (*domain.Run, error)
	ReleaseRun(ctx context.Context, id string) error
	GetRun(ctx context.Context, id string) (domain.Run, error)
	UpdateRunStatus(ctx context.Context, id string, status domain.RunStatus) error
	RecordFailure(ctx context.Context, id, msg string) (int, error)
	SetNeedsReview(ctx context.Context, id, reason string) error
	SetFailed(ctx context.Context, id, reason string) error
	SetOutput(ctx context.Context, id, key, value string) error
	SetAwaitingApproval(ctx context.Context, id string, waiting bool) error
}

// Steps bundles the external adapters. All four are required.
type Steps struct {
	Scripter  Scripter
	Voice     Voice
	Renderer  Renderer
	Publisher Publisher
}

// Event describes one thing the worker did. It is the seed of the Day 9 trace log.
type Event struct {
	RunID    string
	Stage    string
	From, To domain.RunStatus
	Decision agent.Action
	Reason   string
	Err      error
}

// Config tunes a Worker. The zero value is usable.
type Config struct {
	WorkerID    string
	MaxAttempts int // consecutive transient failures, truncations or QA rejections before NEEDS_REVIEW; 0 = DefaultMaxAttempts
	// MaxDrainSteps caps the steps one Drain call may take; 0 = DefaultMaxDrainSteps.
	MaxDrainSteps int
	// Policy is what the script gate holds scripts to. The zero value means a 5
	// minute episode. It must describe the same episode length the Scripter
	// writes, or the gate judges against the wrong word limits.
	Policy policy.Policy
	// Observer, if set, is called for every event. With several workers it is
	// called from several goroutines, so it must be safe for concurrent use.
	Observer func(Event)
}

// Worker claims runs and advances them one step at a time.
type Worker struct {
	store   Store
	decider agent.Decider
	cfg     Config
	stages  map[domain.RunStatus]stage
	gates   map[domain.RunStatus]gate
}

type stage struct {
	name string
	run  func(ctx context.Context, r domain.Run) (map[string]string, error)
}

// gate judges what a step produced, before the run may advance. It never
// repairs anything: a rejection is a failed attempt, counted like any other.
type gate struct {
	key   string // Run.Outputs key the JSON report is kept under
	check func(outputs map[string]string) qa.Report
}

// buildGates maps a status to the gate that judges the step running in it.
// Only the script step has one so far, the others arrive with real adapters.
func buildGates(p policy.Policy) map[domain.RunStatus]gate {
	return map[domain.RunStatus]gate{
		domain.StatusPending: {key: domain.OutputQAScript, check: func(o map[string]string) qa.Report {
			return qa.Script(o[domain.OutputTitle], o[domain.OutputScript], p)
		}},
	}
}

// New returns a Worker. The decider is mandatory: there is no implicit
// permission to act.
func New(store Store, steps Steps, decider agent.Decider, cfg Config) (*Worker, error) {
	switch {
	case store == nil:
		return nil, errors.New("runner: store is required")
	case decider == nil:
		return nil, errors.New("runner: decider is required, there is no implicit permission")
	case steps.Scripter == nil || steps.Voice == nil || steps.Renderer == nil || steps.Publisher == nil:
		return nil, errors.New("runner: scripter, voice, renderer and publisher are all required")
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = DefaultMaxAttempts
	}
	if cfg.MaxDrainSteps <= 0 {
		cfg.MaxDrainSteps = DefaultMaxDrainSteps
	}
	if cfg.WorkerID == "" {
		cfg.WorkerID = fmt.Sprintf("worker-%d", time.Now().UnixNano())
	}
	if cfg.Policy.MaxWords == 0 {
		cfg.Policy = policy.ForDuration(5)
	}
	return &Worker{store: store, decider: decider, cfg: cfg, stages: buildStages(steps), gates: buildGates(cfg.Policy)}, nil
}

// buildStages maps a run's status to the step that runs while it sits there.
func buildStages(s Steps) map[domain.RunStatus]stage {
	need := func(r domain.Run, keys ...string) error {
		for _, k := range keys {
			if r.Outputs[k] == "" {
				return fmt.Errorf("run %s is missing its %q output", r.ID, k)
			}
		}
		return nil
	}
	return map[domain.RunStatus]stage{
		domain.StatusPending: {"script", func(ctx context.Context, r domain.Run) (map[string]string, error) {
			sc, err := s.Scripter.Script(ctx, r.Topic)
			if err != nil {
				return nil, err
			}
			return map[string]string{domain.OutputTitle: sc.Title, domain.OutputScript: sc.Body}, nil
		}},
		domain.StatusScripted: {"voice", func(ctx context.Context, r domain.Run) (map[string]string, error) {
			if err := need(r, domain.OutputScript); err != nil {
				return nil, err
			}
			ref, err := s.Voice.Synthesize(ctx, r.Outputs[domain.OutputScript])
			return map[string]string{domain.OutputVoice: ref}, err
		}},
		domain.StatusVoiced: {"thumbnail", func(ctx context.Context, r domain.Run) (map[string]string, error) {
			ref, err := s.Renderer.Thumbnail(ctx, r.Outputs[domain.OutputTitle])
			return map[string]string{domain.OutputThumbnail: ref}, err
		}},
		domain.StatusThumbnailed: {"render", func(ctx context.Context, r domain.Run) (map[string]string, error) {
			if err := need(r, domain.OutputVoice, domain.OutputThumbnail); err != nil {
				return nil, err
			}
			ref, err := s.Renderer.Render(ctx, r.Outputs[domain.OutputVoice], r.Outputs[domain.OutputThumbnail])
			return map[string]string{domain.OutputVideo: ref}, err
		}},
		domain.StatusRendered: {"package", func(ctx context.Context, r domain.Run) (map[string]string, error) {
			if err := need(r, domain.OutputVideo); err != nil {
				return nil, err
			}
			ref, err := s.Renderer.Package(ctx, r.Outputs[domain.OutputVideo])
			return map[string]string{domain.OutputPackage: ref}, err
		}},
		// In Sleepy this status runs the package QA gate. Until Day 6 it passes through.
		domain.StatusPackaged: {"check", func(context.Context, domain.Run) (map[string]string, error) {
			return nil, nil
		}},
		domain.StatusUploaded: {"publish", func(ctx context.Context, r domain.Run) (map[string]string, error) {
			if err := need(r, domain.OutputPackage); err != nil {
				return nil, err
			}
			res, err := s.Publisher.Publish(ctx, PublishRequest{
				RunID: r.ID, Title: r.Outputs[domain.OutputTitle], VideoRef: r.Outputs[domain.OutputPackage],
			})
			if err != nil {
				return nil, err
			}
			return map[string]string{domain.OutputPublished: res.ID + " (" + res.Visibility + ")"}, nil
		}},
	}
}

// RunOnce claims the next eligible run and processes exactly one step of it.
// It reports whether a run was claimed. A step that fails is recorded on the
// run, it is not an error here; errors are for the store failing or the
// context being cancelled.
func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	run, err := w.store.ClaimNextRun(ctx, w.cfg.WorkerID)
	if err != nil {
		return false, fmt.Errorf("claim: %w", err)
	}
	if run == nil {
		return false, nil
	}
	// Release even when ctx is already cancelled, or the run stays locked
	// until the TTL runs out.
	defer w.store.ReleaseRun(context.WithoutCancel(ctx), run.ID) //nolint:errcheck // best effort, the TTL is the backstop
	return true, w.process(ctx, *run)
}

// Drain processes steps until no run is eligible. Runs waiting for approval
// are not eligible, so Drain returns when everything is done or parked.
func (w *Worker) Drain(ctx context.Context) error {
	for i := 0; i < w.cfg.MaxDrainSteps; i++ {
		did, err := w.RunOnce(ctx)
		if err != nil || !did {
			return err
		}
	}
	return fmt.Errorf("runner: drain stopped after %d steps, is a run looping?", w.cfg.MaxDrainSteps)
}

// Run is the long-running loop: drain, sleep poll, repeat until ctx is done.
func (w *Worker) Run(ctx context.Context, poll time.Duration) error {
	for {
		did, err := w.RunOnce(ctx)
		if err != nil {
			return err
		}
		if did {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

func (w *Worker) process(ctx context.Context, run domain.Run) error {
	st, ok := w.stages[run.Status]
	if !ok {
		reason := fmt.Sprintf("no step for status %s", run.Status)
		w.emit(Event{RunID: run.ID, From: run.Status, To: domain.StatusFailed, Reason: reason})
		return w.store.SetFailed(ctx, run.ID, reason)
	}

	d := w.decider.Decide(ctx, agent.Call{
		Tool: toolPrefix + st.name,
		Args: map[string]any{"run_id": run.ID, "stage": st.name, "status": string(run.Status)},
	})
	switch d.Action {
	case agent.Act:
		// go ahead
	case agent.Ask:
		w.emit(Event{RunID: run.ID, Stage: st.name, From: run.Status, To: run.Status, Decision: agent.Ask, Reason: d.Reason})
		return w.store.SetAwaitingApproval(ctx, run.ID, true)
	default:
		// Stop, and any action this code does not know: fail closed.
		reason := fmt.Sprintf("decider stopped %s: %s", st.name, d.Reason)
		w.emit(Event{RunID: run.ID, Stage: st.name, From: run.Status, To: domain.StatusNeedsReview, Decision: agent.Stop, Reason: reason})
		return w.store.SetNeedsReview(ctx, run.ID, reason)
	}

	outputs, err := st.run(ctx, run)
	if err != nil {
		return w.handleFailure(ctx, run, st, err)
	}
	for k, v := range outputs {
		if err := w.store.SetOutput(ctx, run.ID, k, v); err != nil {
			return err
		}
	}
	// The output is stored before it is judged, so a human who gets a rejected
	// run still sees exactly what was rejected. Nothing downstream reads it: the
	// run stays in its status until the gate passes.
	if g, ok := w.gates[run.Status]; ok {
		report := g.check(outputs)
		b, err := json.Marshal(report)
		if err != nil {
			return fmt.Errorf("encode qa report: %w", err)
		}
		if err := w.store.SetOutput(ctx, run.ID, g.key, string(b)); err != nil {
			return err
		}
		if !report.Pass {
			return w.handleRejection(ctx, run, st, report)
		}
	}
	next := domain.NextStatus(run.Status)
	w.emit(Event{RunID: run.ID, Stage: st.name, From: run.Status, To: next, Decision: agent.Act, Reason: d.Reason})
	return w.store.UpdateRunStatus(ctx, run.ID, next)
}

// handleFailure applies the failure policy:
//   - the context was cancelled: not the step's fault, leave the run as it is;
//   - transient error, or a reply cut off at the token limit: stay in the same
//     status and retry, up to MaxAttempts, then park the run for a human. A
//     truncation is retried because the output is stochastic and a model that
//     was cut off once may finish the next time, the same way QA rejections are
//     retried. The retry is the same request, a smarter one is Day 7;
//   - anything else: the run is FAILED, no retry.
func (w *Worker) handleFailure(ctx context.Context, run domain.Run, st stage, err error) error {
	if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return ctx.Err()
	}
	msg := fmt.Sprintf("%s: %v", st.name, err)

	truncated := errors.Is(err, script.ErrTruncated)
	if errs.IsTransient(err) || truncated {
		n, serr := w.store.RecordFailure(ctx, run.ID, msg)
		if serr != nil {
			return serr
		}
		if n >= w.cfg.MaxAttempts {
			reason := fmt.Sprintf("%s failed %d times in a row, last error: %v", st.name, n, err)
			w.emit(Event{RunID: run.ID, Stage: st.name, From: run.Status, To: domain.StatusNeedsReview, Reason: reason, Err: err})
			return w.store.SetNeedsReview(ctx, run.ID, reason)
		}
		kind := "transient failure"
		if truncated {
			kind = "reply cut off at the token limit"
		}
		w.emit(Event{RunID: run.ID, Stage: st.name, From: run.Status, To: run.Status, Reason: fmt.Sprintf("%s %d of %d, will retry", kind, n, w.cfg.MaxAttempts), Err: err})
		return nil
	}

	w.emit(Event{RunID: run.ID, Stage: st.name, From: run.Status, To: domain.StatusFailed, Reason: "permanent error, not retrying", Err: err})
	return w.store.SetFailed(ctx, run.ID, msg)
}

// handleRejection applies the Day 6 policy to a gate that said no: the step
// runs again, up to MaxAttempts failed attempts in a row (rejections and
// errors count the same), then the run is parked for a human. The retry is the
// same request. Feeding the reason back to the model is the Day 7 fix engine.
func (w *Worker) handleRejection(ctx context.Context, run domain.Run, st stage, report qa.Report) error {
	why := report.Summary()
	n, err := w.store.RecordFailure(ctx, run.ID, fmt.Sprintf("%s: qa rejected: %s", st.name, why))
	if err != nil {
		return err
	}
	if n >= w.cfg.MaxAttempts {
		reason := fmt.Sprintf("%s was rejected by QA %d times in a row, last: %s", st.name, n, why)
		w.emit(Event{RunID: run.ID, Stage: st.name, From: run.Status, To: domain.StatusNeedsReview, Reason: reason, Err: errors.New(why)})
		return w.store.SetNeedsReview(ctx, run.ID, reason)
	}
	w.emit(Event{RunID: run.ID, Stage: st.name, From: run.Status, To: run.Status,
		Reason: fmt.Sprintf("qa rejected %d of %d, will retry", n, w.cfg.MaxAttempts), Err: errors.New(why)})
	return nil
}

func (w *Worker) emit(e Event) {
	if w.cfg.Observer != nil {
		w.cfg.Observer(e)
	}
}

func wordCount(s string) int { return len(strings.Fields(s)) }
