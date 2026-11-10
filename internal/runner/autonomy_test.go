package runner_test

import (
	"context"
	"strings"
	"testing"

	"sleepy-agent/internal/agent"
	"sleepy-agent/internal/domain"
	"sleepy-agent/internal/policy"
	"sleepy-agent/internal/runner"
	"sleepy-agent/internal/store"
)

func enoughEvidence() policy.Evidence {
	return policy.Evidence{OwnerLabelled: true, BadScripts: 100, BadLetThrough: 0}
}

func publishCall(id string) agent.Call {
	return agent.Call{Tool: "stage.publish", Args: map[string]any{"run_id": id}}
}

func TestAutonomyGateAllowListStopsAnythingThatIsNotAStage(t *testing.T) {
	gate := runner.AutonomyGate{Runs: store.NewMemory(nil), Autonomy: policy.Autonomy{Level: policy.LevelReview}}
	for _, tool := range []string{"stage.delete_everything", "publish", "", "stage.", "shell"} {
		if d := gate.Decide(context.Background(), agent.Call{Tool: tool}); d.Action != agent.Stop {
			t.Errorf("%q: want stop, got %s (%s)", tool, d.Action, d.Reason)
		}
	}
	for _, stage := range runner.Stages {
		if stage == "publish" {
			continue
		}
		if d := gate.Decide(context.Background(), agent.Call{Tool: "stage." + stage}); d.Action != agent.Act {
			t.Errorf("stage %s: want act, got %s (%s)", stage, d.Action, d.Reason)
		}
	}
}

func TestAutonomyGateAllowListCoversEveryStageTheWorkerRuns(t *testing.T) {
	// A stage missing from runner.Stages would be stopped in production. Run a
	// whole episode under the gate and make sure nothing was stopped.
	ctx := context.Background()
	st := store.NewMemory(nil)
	gate := runner.AutonomyGate{Runs: st, Autonomy: policy.Autonomy{Level: policy.LevelReview}}
	r := newRig(t, st, gate)
	run, _ := st.CreateRun(ctx, "a quiet forest")
	if err := r.worker.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.ApprovePublish(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.worker.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if got := mustRun(t, st, run.ID); got.Status != domain.StatusDone {
		t.Fatalf("want DONE, got %s: %s", got.Status, got.LastError)
	}
	for _, e := range r.events {
		if e.Decision == agent.Stop {
			t.Fatalf("stage %s was stopped: %s", e.Stage, e.Reason)
		}
	}
}

func TestAutonomyGateHaltStopsEveryStageEvenWithApproval(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	run, _ := st.CreateRun(ctx, "a quiet forest")
	_ = st.ApprovePublish(ctx, run.ID)
	gate := runner.AutonomyGate{Runs: st, Autonomy: policy.Autonomy{Level: policy.LevelHalt, Evidence: enoughEvidence()}}
	for _, stage := range runner.Stages {
		if d := gate.Decide(ctx, agent.Call{Tool: "stage." + stage, Args: map[string]any{"run_id": run.ID}}); d.Action != agent.Stop {
			t.Errorf("stage %s under halt: want stop, got %s", stage, d.Action)
		}
	}
}

func TestAutonomyGateReviewAsksUntilApproved(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	run, _ := st.CreateRun(ctx, "a quiet forest")
	gate := runner.AutonomyGate{Runs: st, Autonomy: policy.Autonomy{Level: policy.LevelReview}}
	if d := gate.Decide(ctx, publishCall(run.ID)); d.Action != agent.Ask {
		t.Fatalf("want ask, got %s", d.Action)
	}
	_ = st.ApprovePublish(ctx, run.ID)
	if d := gate.Decide(ctx, publishCall(run.ID)); d.Action != agent.Act {
		t.Fatalf("want act after approval, got %s", d.Action)
	}
	if d := gate.Decide(ctx, publishCall("missing")); d.Action != agent.Stop {
		t.Fatalf("an unreadable run must stop, got %s", d.Action)
	}
}

func TestAutonomyGateUnlistedActsOnlyWhenTheEvidenceIsEnough(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	run, _ := st.CreateRun(ctx, "a quiet forest")

	earned := runner.AutonomyGate{Runs: st, Autonomy: policy.Autonomy{Level: policy.LevelUnlisted, Evidence: enoughEvidence()}}
	if d := earned.Decide(ctx, publishCall(run.ID)); d.Action != agent.Act {
		t.Fatalf("enough evidence: want act, got %s (%s)", d.Action, d.Reason)
	}

	// The Day 9 numbers, labels by a model: asked for, not granted.
	weak := policy.Autonomy{Level: policy.LevelUnlisted, Evidence: policy.Evidence{BadScripts: 15, BadLetThrough: 9}}
	d := runner.AutonomyGate{Runs: st, Autonomy: weak}.Decide(ctx, publishCall(run.ID))
	if d.Action != agent.Ask || !strings.Contains(d.Reason, "evidence is not enough") {
		t.Fatalf("weak evidence: want ask with the reason, got %s (%s)", d.Action, d.Reason)
	}

	// Unreadable run still stops, even at the highest level.
	if d := earned.Decide(ctx, publishCall("missing")); d.Action != agent.Stop {
		t.Fatalf("unreadable run at unlisted: want stop, got %s", d.Action)
	}
}

func TestUnlistedLevelPublishesWithoutApprovalEndToEnd(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	gate := runner.AutonomyGate{Runs: st, Autonomy: policy.Autonomy{Level: policy.LevelUnlisted, Evidence: enoughEvidence()}}
	r := newRig(t, st, gate)
	run, _ := st.CreateRun(ctx, "a quiet forest")
	if err := r.worker.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	got := mustRun(t, st, run.ID)
	if got.Status != domain.StatusDone || got.AwaitingApproval {
		t.Fatalf("want DONE without approval, got %s awaiting=%v", got.Status, got.AwaitingApproval)
	}
	reqs := r.publisher.Requests()
	if len(reqs) != 1 || reqs[0].MaxVisibility != runner.VisibilityUnlisted {
		t.Fatalf("the worker must cap the publish at unlisted, got %+v", reqs)
	}
}

func TestAskedForUnlistedWithWeakEvidenceStillWaitsForTheHuman(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	weak := policy.Autonomy{Level: policy.LevelUnlisted, Evidence: policy.Evidence{BadScripts: 15, BadLetThrough: 9}}
	r := newRig(t, st, runner.AutonomyGate{Runs: st, Autonomy: weak})
	run, _ := st.CreateRun(ctx, "a quiet forest")
	if err := r.worker.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	got := mustRun(t, st, run.ID)
	if got.Status != domain.StatusUploaded || !got.AwaitingApproval || r.publisher.Calls() != 0 {
		t.Fatalf("want UPLOADED, awaiting approval, no publish; got %s awaiting=%v calls=%d", got.Status, got.AwaitingApproval, r.publisher.Calls())
	}
}

func TestAPublisherThatGoesPublicFailsTheRunAndIsNotRetried(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory(nil)
	r := newRig(t, st, runner.AutonomyGate{Runs: st, Autonomy: policy.Autonomy{Level: policy.LevelUnlisted, Evidence: enoughEvidence()}})
	r.publisher.Visibility = "public"
	run, _ := st.CreateRun(ctx, "a quiet forest")
	if err := r.worker.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	got := mustRun(t, st, run.ID)
	if got.Status != domain.StatusFailed {
		t.Fatalf("want FAILED, got %s", got.Status)
	}
	if !strings.Contains(got.LastError, "take it down by hand") || !strings.Contains(got.LastError, "mock:"+run.ID) {
		t.Fatalf("the error must name the publish so a human can remove it: %q", got.LastError)
	}
	if r.publisher.Calls() != 1 {
		t.Fatalf("a failed visibility check must not retry the publish, called %d times", r.publisher.Calls())
	}
}
