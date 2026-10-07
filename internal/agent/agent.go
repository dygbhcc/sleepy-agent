package agent

import (
	"context"
	"encoding/json"
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
func (a *Agent) Run(ctx context.Context, task string) (*Result, error) {
	res := &Result{}
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: a.systemPrompt()},
		{Role: llm.RoleUser, Content: task},
	}

	for i := 1; i <= a.cfg.MaxSteps; i++ {
		if err := ctx.Err(); err != nil {
			return res, err
		}

		resp, err := a.provider.Complete(ctx, llm.Request{
			Messages:    msgs,
			Temperature: a.cfg.Temperature,
			MaxTokens:   a.cfg.MaxTokens,
			JSON:        true,
		})
		if err != nil {
			return res, fmt.Errorf("agent: model call %d failed: %w", i, err)
		}
		res.Usage.PromptTokens += resp.Usage.PromptTokens
		res.Usage.CompletionTokens += resp.Usage.CompletionTokens
		msgs = append(msgs, llm.Message{Role: llm.RoleAssistant, Content: resp.Content})

		reply, err := parseReply(resp.Content)
		if err != nil {
			obs := fmt.Sprintf("invalid reply: %v. Reply with exactly one JSON object.", err)
			res.Steps = append(res.Steps, Step{Index: i, Observation: obs})
			msgs = append(msgs, observation(obs))
			continue
		}
		if reply.final != nil {
			res.Outcome = Finished
			res.Final = *reply.final
			return res, nil
		}

		call := Call{Tool: reply.tool, Args: reply.args}
		step := Step{Index: i, Call: &call}

		tool, ok := a.tools[call.Tool]
		if !ok {
			step.Observation = fmt.Sprintf("unknown tool %q. Available tools: %s", call.Tool, strings.Join(a.toolNames(), ", "))
			res.Steps = append(res.Steps, step)
			msgs = append(msgs, observation(step.Observation))
			continue
		}
		if err := tool.Schema().Validate(call.Args); err != nil {
			step.Observation = fmt.Sprintf("invalid arguments for %s: %v", call.Tool, err)
			res.Steps = append(res.Steps, step)
			msgs = append(msgs, observation(step.Observation))
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
		msgs = append(msgs, observation(step.Observation))
	}

	res.Outcome = StepLimit
	return res, nil
}

func observation(s string) llm.Message {
	return llm.Message{Role: llm.RoleUser, Content: "Observation: " + s}
}

func (a *Agent) toolNames() []string {
	names := make([]string, 0, len(a.tools))
	for n := range a.tools {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (a *Agent) systemPrompt() string {
	var b strings.Builder
	b.WriteString("You are an agent that completes a task by calling tools.\n")
	b.WriteString("Reply with exactly one JSON object and nothing else.\n")
	b.WriteString(`To call a tool: {"tool": "<name>", "args": {...}}` + "\n")
	b.WriteString(`To finish: {"final": "<your answer>"}` + "\n")
	b.WriteString("After each tool call you will get an Observation. Use it to decide the next step.\n\nTools:\n")
	for _, name := range a.toolNames() {
		t := a.tools[name]
		fmt.Fprintf(&b, "- %s: %s\n", name, t.Description())
		props := t.Schema().Properties
		keys := make([]string, 0, len(props))
		for k := range props {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "    %s (%s): %s\n", k, props[k].Type, props[k].Description)
		}
	}
	return b.String()
}

type reply struct {
	tool  string
	args  map[string]any
	final *string
}

// parseReply reads the first JSON object in the model's answer. Models often
// wrap JSON in code fences or add a sentence around it, so the parser looks
// for the first '{' instead of demanding a clean string.
func parseReply(content string) (reply, error) {
	start := strings.Index(content, "{")
	if start < 0 {
		return reply{}, errors.New("no JSON object found")
	}
	var raw struct {
		Tool  string         `json:"tool"`
		Args  map[string]any `json:"args"`
		Final *string        `json:"final"`
	}
	if err := json.NewDecoder(strings.NewReader(content[start:])).Decode(&raw); err != nil {
		return reply{}, fmt.Errorf("malformed JSON: %v", err)
	}
	switch {
	case raw.Tool != "" && raw.Final != nil:
		return reply{}, errors.New(`got both "tool" and "final"`)
	case raw.Final != nil:
		return reply{final: raw.Final}, nil
	case raw.Tool != "":
		if raw.Args == nil {
			raw.Args = map[string]any{}
		}
		return reply{tool: raw.Tool, args: raw.Args}, nil
	default:
		return reply{}, errors.New(`need either "tool" or "final"`)
	}
}
