// Package runner drives runs through the pipeline state machine.
//
// It is the Sleepy worker (internal/jobs/worker.go) reduced to its skeleton:
// claim a run, process exactly ONE step, release it, loop. Because the status
// is the only memory, a run that dies in the middle resumes from the step it
// was on. What Sleepy bolts onto this loop arrives later: the shadow reasoner
// on Day 8, guardrails on Day 9. Since Day 6 a step's output goes through a QA
// gate before the run may advance. Since Day 7 a rejection selects a directed
// fix from the fix engine for the next attempt, and an accepted script is
// cached by input hash so the same input is never paid for twice.
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
	"sleepy-agent/internal/fix"
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
	// Fixes is the fix engine. With it, a gate rejection chooses a directed
	// repair for the next attempt; without it (nil) the next attempt repeats
	// the same request, which is the Day 6 behaviour and the baseline the fix
	// engine is measured against.
	Fixes *fix.Engine
	// Cache keeps accepted scripts by input hash. Nil means no caching, and so
	// does a Scripter that is not a Fingerprinter.
	Cache ScriptCache
	// Shadow reviews every script the gate accepts and records its verdict in
	// the run. It has no power: a verdict, an error or a timeout never changes
	// what the run does next. Nil means no review.
	Shadow Reviewer
	// ShadowTimeout bounds one review; 0 means 2 minutes.
	ShadowTimeout time.Duration
	// Observer, if set, is called for every event. With several workers it is
	// called from several goroutines, so it must be safe for concurrent use.
	Observer func(Event)
}

// Worker claims runs and advances them one step at a time.
type Worker struct {
	store    Store
	decider  agent.Decider
	cfg      Config
	scripter Scripter
	stages   map[domain.RunStatus]stage
	gates    map[domain.RunStatus]gate
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
	return &Worker{store: store, decider: decider, cfg: cfg, scripter: steps.Scripter,
		stages: buildStages(steps, cfg.Cache), gates: buildGates(cfg.Policy)}, nil
}

// buildStages maps a run's status to the step that runs while it sits there.
func buildStages(s Steps, cache ScriptCache) map[domain.RunStatus]stage {
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
			out := func(sc Script, source string) map[string]string {
				return map[string]string{domain.OutputTitle: sc.Title, domain.OutputScript: sc.Body, domain.OutputScriptSource: source}
			}
			// A fix is in play: the previous attempt was rejected and its output
			// is still on the run, so the plan can build on it.
			if fs, ok := readFixState(r.Outputs); ok {
				if adj, ok := s.Scripter.(AdjustableScripter); ok {
					a := fs.Plan.Adjust(r.Outputs[domain.OutputTitle], r.Outputs[domain.OutputScript])
					a.Style = r.Style
					sc, err := adj.ScriptAdjusted(ctx, r.Topic, a)
					if err != nil {
						return nil, err
					}
					return out(sc, domain.SourceModel), nil
				}
			}
			// Same input as a script the gate already accepted: no model call. The
			// gate still judges it, so a changed policy is not bypassed.
			if key, ok := scriptCacheKey(s.Scripter, cache, r.Topic, r.Style); ok {
				if sc, hit, err := cache.Get(key); err == nil && hit {
					return out(sc, domain.SourceCache), nil
				}
			}
			var sc Script
			var err error
			if adj, ok := s.Scripter.(AdjustableScripter); ok {
				sc, err = adj.ScriptAdjusted(ctx, r.Topic, script.Adjust{Style: r.Style})
			} else {
				sc, err = s.Scripter.Script(ctx, r.Topic)
			}
			if err != nil {
				return nil, err
			}
			return out(sc, domain.SourceModel), nil
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
	var note string // what to add to the event when the run advances
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
			w.forgetRejectedScript(run, outputs)
			return w.handleRejection(ctx, run, st, report)
		}
		applied, err := w.settleFix(ctx, run)
		if err != nil {
			return err
		}
		if applied != "" {
			note = "qa passed after fix " + applied
		}
		if run.Status == domain.StatusPending {
			if msg := w.shadowReview(ctx, run, outputs); msg != "" {
				note = joinNotes(note, msg)
			}
			if outputs[domain.OutputScriptSource] == domain.SourceCache {
				note = joinNotes(note, "script served from cache, no model call")
			} else {
				w.rememberAcceptedScript(run, outputs)
			}
		}
	}
	next := domain.NextStatus(run.Status)
	reason := d.Reason
	if note != "" {
		reason = strings.TrimSpace(reason + "; " + note)
	}
	w.emit(Event{RunID: run.ID, Stage: st.name, From: run.Status, To: next, Decision: agent.Act, Reason: reason})
	return w.store.UpdateRunStatus(ctx, run.ID, next)
}

// handleFailure applies the failure policy:
//   - the context was cancelled: not the step's fault, leave the run as it is;
//   - transient error, or a reply cut off at the token limit: stay in the same
//     status and retry, up to MaxAttempts, then park the run for a human. A
//     truncation is retried because the output is stochastic and a model that
//     was cut off once may finish the next time, the same way QA rejections are
//     retried. The retry is the same request: the fix engine has no plan for
//     these yet, and an attempt that fixed nothing keeps the plan it had;
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

// handleRejection applies the policy to a gate that said no: the step runs
// again, up to MaxAttempts failed attempts in a row (rejections and errors
// count the same), then the run is parked for a human.
//
// With a fix engine the next attempt is a directed repair chosen from the
// report, not the same request: the plan that was just applied is scored on
// whether it cured the failure it was chosen for, and the next untried plan for
// the failure the gate reports now is stored on the run. Without an engine, or
// when every plan for the failure was tried, the retry is the same request.
func (w *Worker) handleRejection(ctx context.Context, run domain.Run, st stage, report qa.Report) error {
	why := report.Summary()
	n, err := w.store.RecordFailure(ctx, run.ID, fmt.Sprintf("%s: qa rejected: %s", st.name, why))
	if err != nil {
		return err
	}
	state, had := readFixState(run.Outputs)
	if had && w.cfg.Fixes != nil {
		w.cfg.Fixes.Record(fix.Outcome{PlanID: state.Plan.ID, Success: fix.Fixed(report, state.Plan.FailType), AttemptsToPass: n})
	}
	if n >= w.cfg.MaxAttempts {
		reason := fmt.Sprintf("%s was rejected by QA %d times in a row, last: %s", st.name, n, why)
		if len(state.Tried) > 0 {
			reason += fmt.Sprintf(" (fixes tried: %s)", strings.Join(state.Tried, ", "))
		}
		w.emit(Event{RunID: run.ID, Stage: st.name, From: run.Status, To: domain.StatusNeedsReview, Reason: reason, Err: errors.New(why)})
		return w.store.SetNeedsReview(ctx, run.ID, reason)
	}

	if w.cfg.Fixes != nil && st.name == "script" {
		if plan, ok := w.cfg.Fixes.Decide(report, state.Tried); ok {
			next := fixState{Plan: plan, Tried: append(append([]string(nil), state.Tried...), plan.ID)}
			b, err := json.Marshal(next)
			if err != nil {
				return fmt.Errorf("encode fix state: %w", err)
			}
			if err := w.store.SetOutput(ctx, run.ID, domain.OutputFixState, string(b)); err != nil {
				return err
			}
			w.emit(Event{RunID: run.ID, Stage: st.name, From: run.Status, To: run.Status,
				Reason: fmt.Sprintf("qa rejected %d of %d, will retry with fix %s", n, w.cfg.MaxAttempts, plan.ID), Err: errors.New(why)})
			return nil
		}
	}
	// No plan: a plain retry, and the old plan is not applied again.
	if had {
		if err := w.store.SetOutput(ctx, run.ID, domain.OutputFixState, ""); err != nil {
			return err
		}
	}
	w.emit(Event{RunID: run.ID, Stage: st.name, From: run.Status, To: run.Status,
		Reason: fmt.Sprintf("qa rejected %d of %d, will retry", n, w.cfg.MaxAttempts), Err: errors.New(why)})
	return nil
}

// fixState is what the worker remembers about the fix engine on a run. It
// lives in the run's outputs, so a crashed worker's successor still knows which
// plans were tried.
type fixState struct {
	Plan  fix.Plan `json:"plan"`  // applied to the attempt that is about to run, or has just run
	Tried []string `json:"tried"` // every plan applied to this run, in order
}

// readFixState reads the run's fix state. An empty or unreadable value means
// no fix is in play.
func readFixState(outputs map[string]string) (fixState, bool) {
	raw := outputs[domain.OutputFixState]
	if raw == "" {
		return fixState{}, false
	}
	var fs fixState
	if err := json.Unmarshal([]byte(raw), &fs); err != nil || fs.Plan.ID == "" {
		return fixState{}, false
	}
	return fs, true
}

// settleFix runs when the gate passes. If a fix was applied to this attempt it
// is credited, and the fix state is cleared. It returns the ID of the plan that
// was applied, "" when none.
func (w *Worker) settleFix(ctx context.Context, run domain.Run) (string, error) {
	state, had := readFixState(run.Outputs)
	if !had {
		return "", nil
	}
	if w.cfg.Fixes != nil {
		w.cfg.Fixes.Record(fix.Outcome{PlanID: state.Plan.ID, Success: true, AttemptsToPass: run.FailedAttempts + 1})
	}
	return state.Plan.ID, w.store.SetOutput(ctx, run.ID, domain.OutputFixState, "")
}

// scriptCacheKey is the input hash of a topic for this scripter. It reports
// false when there is no cache or the scripter cannot say what it is.
func scriptCacheKey(sc Scripter, cache ScriptCache, topic, style string) (string, bool) {
	if cache == nil {
		return "", false
	}
	fp, ok := sc.(Fingerprinter)
	if !ok {
		return "", false
	}
	f := fp.Fingerprint()
	if f == "" {
		return "", false
	}
	if style != "" {
		f += "|style=" + style // a run's own style changes what is asked
	}
	return CacheKey(f, topic), true
}

// rememberAcceptedScript caches a script the gate accepted. Best effort: a
// cache that cannot write only costs a model call next time.
func (w *Worker) rememberAcceptedScript(run domain.Run, outputs map[string]string) {
	key, ok := scriptCacheKey(w.scripter, w.cfg.Cache, run.Topic, run.Style)
	if !ok {
		return
	}
	if err := w.cfg.Cache.Put(key, Script{Title: outputs[domain.OutputTitle], Body: outputs[domain.OutputScript]}); err != nil {
		w.emit(Event{RunID: run.ID, Stage: "script", From: run.Status, To: run.Status, Reason: "could not write the script cache", Err: err})
	}
}

// forgetRejectedScript evicts a cached script the gate no longer accepts, for
// example after the policy changed. Otherwise every retry would be served the
// same rejected script.
func (w *Worker) forgetRejectedScript(run domain.Run, outputs map[string]string) {
	if run.Status != domain.StatusPending || outputs[domain.OutputScriptSource] != domain.SourceCache {
		return
	}
	if key, ok := scriptCacheKey(w.scripter, w.cfg.Cache, run.Topic, run.Style); ok {
		_ = w.cfg.Cache.Delete(key) //nolint:errcheck // best effort, a stale entry is rejected again and evicted again
	}
}

// shadowReview asks the shadow reviewer about an accepted script and records
// the verdict. It returns a short note for the event, empty when there is no
// reviewer. Nothing it does can fail the step: a review that errors or times
// out is reported in the note and the run goes on exactly as it would have.
// A script served from the cache is not reviewed again: it was reviewed, if a
// reviewer was set, when the model wrote it.
func (w *Worker) shadowReview(ctx context.Context, run domain.Run, outputs map[string]string) string {
	if w.cfg.Shadow == nil || outputs[domain.OutputScriptSource] == domain.SourceCache {
		return ""
	}
	timeout := w.cfg.ShadowTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	v, err := w.cfg.Shadow.Review(rctx, run.Topic, outputs[domain.OutputTitle], outputs[domain.OutputScript])
	if err != nil {
		return "shadow review failed: " + oneLine(err.Error())
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "shadow review not recorded: " + oneLine(err.Error())
	}
	if err := w.store.SetOutput(ctx, run.ID, domain.OutputShadowVerdict, string(b)); err != nil {
		return "shadow review not recorded: " + oneLine(err.Error())
	}
	if v.Pass {
		return fmt.Sprintf("shadow review: pass (calm %d, coherence %d, variety %d)", v.Calm, v.Coherence, v.Variety)
	}
	return fmt.Sprintf("shadow review: would reject (calm %d, coherence %d, variety %d): %s", v.Calm, v.Coherence, v.Variety, oneLine(v.Reason))
}

func joinNotes(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	return s
}

func (w *Worker) emit(e Event) {
	if w.cfg.Observer != nil {
		w.cfg.Observer(e)
	}
}

func wordCount(s string) int { return len(strings.Fields(s)) }
