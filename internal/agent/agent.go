package agent

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"sleepy-agent/internal/llm"
)

// Outcome is how a run ended.
type Outcome string

const (
	// Finished means the model produced a final answer.
	Finished Outcome = "finished"
	// NeedsApproval means the Decider asked for a human. Result.Pending holds
	// the call that was not run.
	NeedsApproval Outcome = "needs_approval"
	// Stopped means the Decider halted the run.
	Stopped Outcome = "stopped"
	// StepLimit means the model never finished within Config.MaxSteps.
	StepLimit Outcome = "step_limit"
)

// Step is one entry in the run trace.
type Step struct {
	Index       int       // 1 based position, one per model call
	Call        *Call     // nil when the model's reply was not a valid call
	Decision    *Decision // nil when no Decider was consulted
	Executed    bool      // true only if the tool actually ran
	Observation string    // what was fed back to the model
}

// Result is everything a run produced. It is returned even when the run fails,
// so cost and trace are never lost.
type Result struct {
	Outcome   Outcome
	Final     string
	Pending   *Call // set for NeedsApproval and Stopped
	Steps     []Step
	ToolCalls int // tool calls that actually ran
	Usage     llm.Usage
}

// Config tunes a run. Zero values pick safe defaults.
type Config struct {
	MaxSteps    int     // model calls allowed per run, default 8
	Temperature float64 // default 0, agents should be as repeatable as possible
	MaxTokens   int     // per model call, default 1024
}

// Agent runs the loop.
type Agent struct {
	provider llm.Provider
	tools    map[string]Tool
	decider  Decider
	cfg      Config
}

// New builds an Agent. A Decider is mandatory: there is no implicit permission.
func New(p llm.Provider, tools []Tool, d Decider, cfg Config) (*Agent, error) {
	if p == nil {
		return nil, errors.New("agent: provider is required")
	}
	if d == nil {
		return nil, errors.New("agent: decider is required")
	}
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = 8
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 1024
	}
	byName := make(map[string]Tool, len(tools))
	for _, t := range tools {
		if _, dup := byName[t.Name()]; dup {
			return nil, fmt.Errorf("agent: duplicate tool %q", t.Name())
		}
		byName[t.Name()] = t
	}
	return &Agent{provider: p, tools: byName, decider: d, cfg: cfg}, nil
}

// Run executes the loop for one task.
//
// The agent speaks one normalized format (llm.ToolCall in, llm.RoleTool out).
// Whether the model underneath uses native tool calling or a JSON-in-prompt
// protocol is the adapter's business, see llm.PromptTools.
func (a *Agent) Run(ctx context.Context, task string) (*Result, error) {
	res := &Result{}
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: systemPrompt},
		{Role: llm.RoleUser, Content: task},
	}
	specs := a.specs()

	for i := 1; i <= a.cfg.MaxSteps; i++ {
		if err := ctx.Err(); err != nil {
			return res, err
		}

		resp, err := a.provider.Complete(ctx, llm.Request{
			Messages:    msgs,
			Temperature: a.cfg.Temperature,
			MaxTokens:   a.cfg.MaxTokens,
			Tools:       specs,
		})
		res.Usage.PromptTokens += resp.Usage.PromptTokens
		res.Usage.CompletionTokens += resp.Usage.CompletionTokens
		if errors.Is(err, llm.ErrMalformedReply) {
			obs := "invalid reply: " + strings.TrimPrefix(err.Error(), llm.ErrMalformedReply.Error()+": ")
			res.Steps = append(res.Steps, Step{Index: i, Observation: obs})
			msgs = append(msgs, llm.Message{Role: llm.RoleAssistant, Content: resp.Content}, userNote(obs))
			continue
		}
		if err != nil {
			return res, fmt.Errorf("agent: model call %d failed: %w", i, err)
		}

		if len(resp.ToolCalls) == 0 {
			if strings.TrimSpace(resp.Content) == "" {
				obs := "invalid reply: empty answer. Call a tool or give a final answer."
				res.Steps = append(res.Steps, Step{Index: i, Observation: obs})
				msgs = append(msgs, llm.Message{Role: llm.RoleAssistant}, userNote(obs))
				continue
			}
			res.Outcome = Finished
			res.Final = resp.Content
			return res, nil
		}

		// One call per step. Extra calls in the same reply are dropped, which
		// keeps every assistant tool call paired with exactly one result.
		tc := resp.ToolCalls[0]
		msgs = append(msgs, llm.Message{Role: llm.RoleAssistant, Content: resp.Content, ToolCalls: []llm.ToolCall{tc}})
		reply := func(obs string) {
			msgs = append(msgs, llm.Message{Role: llm.RoleTool, ToolCallID: tc.ID, Content: obs})
		}

		call := Call{Tool: tc.Name, Args: tc.Args}
		step := Step{Index: i, Call: &call}

		tool, ok := a.tools[call.Tool]
		if !ok {
			step.Observation = fmt.Sprintf("unknown tool %q. Available tools: %s", call.Tool, strings.Join(a.toolNames(), ", "))
			res.Steps = append(res.Steps, step)
			reply(step.Observation)
			continue
		}
		if err := tool.Schema().Validate(call.Args); err != nil {
			step.Observation = fmt.Sprintf("invalid arguments for %s: %v", call.Tool, err)
			res.Steps = append(res.Steps, step)
			reply(step.Observation)
			continue
		}

		dec := a.decider.Decide(ctx, call)
		step.Decision = &dec
		switch dec.Action {
		case Act:
			// fall through and run the tool
		case Ask:
			step.Observation = "approval required: " + dec.Reason
			res.Steps = append(res.Steps, step)
			res.Outcome, res.Pending = NeedsApproval, &call
			return res, nil
		default: // Stop, and anything unknown fails closed
			step.Observation = "stopped: " + dec.Reason
			res.Steps = append(res.Steps, step)
			res.Outcome, res.Pending = Stopped, &call
			return res, nil
		}

		out, err := tool.Run(ctx, call.Args)
		step.Executed = true
		res.ToolCalls++
		if err != nil {
			step.Observation = "error: " + err.Error()
		} else {
			step.Observation = out
		}
		res.Steps = append(res.Steps, step)
		reply(step.Observation)
	}

	res.Outcome = StepLimit
	return res, nil
}

func userNote(s string) llm.Message { return llm.Message{Role: llm.RoleUser, Content: s} }

// specs describes the tools to the model, sorted so requests are repeatable.
func (a *Agent) specs() []llm.ToolSpec {
	specs := make([]llm.ToolSpec, 0, len(a.tools))
	for _, name := range a.toolNames() {
		t := a.tools[name]
		specs = append(specs, llm.ToolSpec{Name: name, Description: t.Description(), Parameters: t.Schema().JSONSchema()})
	}
	return specs
}

func (a *Agent) toolNames() []string {
	names := make([]string, 0, len(a.tools))
	for n := range a.tools {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// systemPrompt is the same for every model. The adapter adds whatever its
// protocol needs on top (the JSON format for prompt based tool calling).
const systemPrompt = "You are an agent that completes a task by calling tools. " +
	"Use the tools to get facts, do not guess them. When you have what you need, give a short final answer."
