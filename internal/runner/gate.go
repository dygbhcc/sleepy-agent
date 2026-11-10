package runner

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"sleepy-agent/internal/agent"
	"sleepy-agent/internal/domain"
	"sleepy-agent/internal/policy"
)

// RunGetter is the part of a Store the approval gate reads.
type RunGetter interface {
	GetRun(ctx context.Context, id string) (domain.Run, error)
}

// PublishGate is the Day 4 autonomy policy, written as an agent.Decider.
//
// Every step except publishing is allowed. Publishing needs a human's yes
// recorded on the run. If the run cannot be read, the answer is stop: the gate
// fails closed. AutonomyGate is the Day 10 policy that grew from it.
type PublishGate struct {
	Runs RunGetter
}

// Decide implements agent.Decider.
func (g PublishGate) Decide(ctx context.Context, call agent.Call) agent.Decision {
	if call.Tool != toolPrefix+"publish" {
		return agent.Decision{Action: agent.Act, Reason: "not a publish step"}
	}
	id, _ := call.Args["run_id"].(string)
	run, err := g.Runs.GetRun(ctx, id)
	if err != nil {
		return agent.Decision{Action: agent.Stop, Reason: "cannot read run, failing closed: " + err.Error()}
	}
	if run.Approved {
		return agent.Decision{Action: agent.Act, Reason: "publish approved"}
	}
	return agent.Decision{Action: agent.Ask, Reason: "publishing needs human approval"}
}

// Stages are the steps the worker may run, in pipeline order. A call to
// anything else is not on the allow-list and is stopped.
var Stages = []string{"script", "voice", "thumbnail", "render", "package", "check", "publish"}

// AutonomyGate is the Day 10 policy, written as an agent.Decider. It replaces
// PublishGate where an autonomy policy file is in use.
//
//   - A tool that is not a known stage is stopped (allow-list).
//   - Level halt stops everything.
//   - Level review runs every step except publishing, which needs the human's
//     yes recorded on the run.
//   - Level unlisted also lets a publish through without the yes, but only if
//     the code grants it from the evidence, see policy.Autonomy.Granted. If the
//     evidence is not enough the gate behaves as level review and says why.
//
// If the run cannot be read when publishing, the answer is stop: it fails
// closed. The publish itself is capped at unlisted by the worker.
type AutonomyGate struct {
	Runs     RunGetter
	Autonomy policy.Autonomy
}

// Decide implements agent.Decider.
func (g AutonomyGate) Decide(ctx context.Context, call agent.Call) agent.Decision {
	level, why := g.Autonomy.Granted()
	if level == policy.LevelHalt {
		return agent.Decision{Action: agent.Stop, Reason: "halted: " + why}
	}
	stage, ok := strings.CutPrefix(call.Tool, toolPrefix)
	if !ok || !slices.Contains(Stages, stage) {
		return agent.Decision{Action: agent.Stop, Reason: fmt.Sprintf("%q is not on the allow-list, failing closed", call.Tool)}
	}
	if stage != "publish" {
		return agent.Decision{Action: agent.Act, Reason: "not a publish step"}
	}
	id, _ := call.Args["run_id"].(string)
	run, err := g.Runs.GetRun(ctx, id)
	if err != nil {
		return agent.Decision{Action: agent.Stop, Reason: "cannot read run, failing closed: " + err.Error()}
	}
	if run.Approved {
		return agent.Decision{Action: agent.Act, Reason: "publish approved"}
	}
	if level == policy.LevelUnlisted {
		return agent.Decision{Action: agent.Act, Reason: "level unlisted, evidence is enough, publish is capped at unlisted"}
	}
	reason := "publishing needs human approval"
	if why != "" {
		reason += " (" + why + ")"
	}
	return agent.Decision{Action: agent.Ask, Reason: reason}
}
