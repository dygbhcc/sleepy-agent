package agent

import "context"

// Action is what the Decider says to do with a proposed tool call.
type Action int

const (
	// Act lets the call run.
	Act Action = iota
	// Ask pauses the run until a human approves the call.
	Ask
	// Stop halts the run.
	Stop
)

func (a Action) String() string {
	switch a {
	case Act:
		return "act"
	case Ask:
		return "ask"
	case Stop:
		return "stop"
	default:
		return "unknown"
	}
}

// Call is a tool call proposed by the model.
type Call struct {
	Tool string
	Args map[string]any
}

// Decision is the Decider's verdict on one Call.
type Decision struct {
	Action Action
	Reason string
}

// Decider owns the question "may the agent do this?". It is consulted before
// every tool call. Today a rule can answer it; later an LLM can advise, first
// in shadow mode, while the rule keeps the final say.
type Decider interface {
	Decide(ctx context.Context, call Call) Decision
}

// DeciderFunc adapts a function to a Decider.
type DeciderFunc func(ctx context.Context, call Call) Decision

// Decide implements Decider.
func (f DeciderFunc) Decide(ctx context.Context, call Call) Decision { return f(ctx, call) }

// AlwaysAct approves every call. It is the Day 3 default, the real policy
// arrives with the approval gate.
type AlwaysAct struct{}

// Decide implements Decider.
func (AlwaysAct) Decide(context.Context, Call) Decision {
	return Decision{Action: Act, Reason: "always act"}
}
