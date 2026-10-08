package runner

import (
	"context"

	"sleepy-agent/internal/agent"
	"sleepy-agent/internal/domain"
)

// RunGetter is the part of a Store the approval gate reads.
type RunGetter interface {
	GetRun(ctx context.Context, id string) (domain.Run, error)
}

// PublishGate is the Day 4 autonomy policy, written as an agent.Decider.
//
// Every step except publishing is allowed. Publishing needs a human's yes
// recorded on the run. If the run cannot be read, the answer is stop: the gate
// fails closed. The Day 9 policy grows from here (stale approvals, budgets,
// autonomy levels).
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
