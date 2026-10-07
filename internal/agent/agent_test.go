package agent_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"sleepy-agent/internal/agent"
	"sleepy-agent/internal/llm"
	"sleepy-agent/internal/tools"
)

// spyTool counts how often it really runs.
type spyTool struct {
	name string
	runs atomic.Int32
	fail error
}

func (s *spyTool) Name() string        { return s.name }
func (s *spyTool) Description() string { return "a spy" }
func (s *spyTool) Schema() agent.Schema {
	return agent.Schema{
		Properties: map[string]agent.Prop{"n": {Type: "integer", Description: "a number"}},
		Required:   []string{"n"},
	}
}
func (s *spyTool) Run(context.Context, map[string]any) (string, error) {
	s.runs.Add(1)
	if s.fail != nil {
		return "", s.fail
	}
	return "ok", nil
}

func newAgent(t *testing.T, p llm.Provider, d agent.Decider, tl ...agent.Tool) *agent.Agent {
	t.Helper()
	a, err := agent.New(p, tl, d, agent.Config{MaxSteps: 5})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func lastUserMessage(m *llm.Mock) string {
	reqs := m.Requests()
	msgs := reqs[len(reqs)-1].Messages
	return msgs[len(msgs)-1].Content
}

func TestRunsThreeToolsInOrderThenFinishes(t *testing.T) {
	m := llm.NewMock(
		`{"tool":"get_policy","args":{"duration_minutes":5}}`,
		`{"tool":"count_words","args":{"text":"one two three"}}`,
		`{"tool":"check_repetition","args":{"text":"A calm night. A calm night."}}`,
		`{"final":"too short"}`,
	)
	a := newAgent(t, m, agent.AlwaysAct{}, tools.All()...)

	res, err := a.Run(context.Background(), "check the draft")
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != agent.Finished || res.Final != "too short" {
		t.Fatalf("outcome %q final %q", res.Outcome, res.Final)
	}
	var order []string
	for _, s := range res.Steps {
		order = append(order, s.Call.Tool)
	}
	if got := strings.Join(order, ","); got != "get_policy,count_words,check_repetition" {
		t.Fatalf("tool order = %s", got)
	}
	if res.ToolCalls != 3 {
		t.Fatalf("ToolCalls = %d, want 3", res.ToolCalls)
	}
	if res.Steps[1].Observation != "3" {
		t.Fatalf("count_words observation = %q, want 3", res.Steps[1].Observation)
	}
	if res.Usage.PromptTokens == 0 || res.Usage.CompletionTokens == 0 {
		t.Fatalf("usage should be accumulated, got %+v", res.Usage)
	}
}

func TestToolResultIsFedBackToTheModel(t *testing.T) {
	m := llm.NewMock(`{"tool":"count_words","args":{"text":"a b c d"}}`, `{"final":"done"}`)
	a := newAgent(t, m, agent.AlwaysAct{}, tools.CountWords{})
	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if got := lastUserMessage(m); got != "Observation: 4" {
		t.Fatalf("second request should end with the observation, got %q", got)
	}
}

func TestInvalidArgumentsBecomeAnObservationAndToolDoesNotRun(t *testing.T) {
	spy := &spyTool{name: "spy"}
	m := llm.NewMock(
		`{"tool":"spy","args":{"n":"five"}}`,      // wrong type
		`{"tool":"spy","args":{"n":5,"extra":1}}`, // unknown argument
		`{"tool":"spy","args":{}}`,                // missing argument
		`{"tool":"spy","args":{"n":5}}`,
		`{"final":"ok"}`,
	)
	a := newAgent(t, m, agent.AlwaysAct{}, spy)

	res, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"must be of type integer", "unknown argument", "missing required argument"} {
		if obs := res.Steps[i].Observation; !strings.Contains(obs, want) || res.Steps[i].Executed {
			t.Fatalf("step %d: observation %q executed=%v, want %q and not executed", i, obs, res.Steps[i].Executed, want)
		}
	}
	if spy.runs.Load() != 1 || res.ToolCalls != 1 {
		t.Fatalf("tool ran %d times (ToolCalls %d), want exactly 1", spy.runs.Load(), res.ToolCalls)
	}
	if res.Outcome != agent.Finished {
		t.Fatalf("the model should recover, outcome %q", res.Outcome)
	}
}

func TestToolErrorIsAnObservation(t *testing.T) {
	spy := &spyTool{name: "spy", fail: errors.New("disk full")}
	m := llm.NewMock(`{"tool":"spy","args":{"n":1}}`, `{"final":"gave up"}`)
	a := newAgent(t, m, agent.AlwaysAct{}, spy)

	res, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Steps[0].Observation; got != "error: disk full" {
		t.Fatalf("observation = %q", got)
	}
	if !res.Steps[0].Executed || res.ToolCalls != 1 {
		t.Fatal("a tool that ran and failed still counts as a tool call")
	}
}

func TestUnknownToolListsTheAvailableOnes(t *testing.T) {
	m := llm.NewMock(`{"tool":"rm_rf","args":{}}`, `{"final":"ok"}`)
	a := newAgent(t, m, agent.AlwaysAct{}, tools.All()...)

	res, _ := a.Run(context.Background(), "go")
	obs := res.Steps[0].Observation
	if !strings.Contains(obs, `unknown tool "rm_rf"`) || !strings.Contains(obs, "count_words") {
		t.Fatalf("observation = %q", obs)
	}
	if res.ToolCalls != 0 {
		t.Fatal("an unknown tool must not run")
	}
}

func TestDeciderAskPausesAndDoesNotRunTheTool(t *testing.T) {
	spy := &spyTool{name: "spy"}
	m := llm.NewMock(`{"tool":"spy","args":{"n":1}}`)
	ask := agent.DeciderFunc(func(context.Context, agent.Call) agent.Decision {
		return agent.Decision{Action: agent.Ask, Reason: "publishing needs a human"}
	})
	a := newAgent(t, m, ask, spy)

	res, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != agent.NeedsApproval || res.Pending == nil || res.Pending.Tool != "spy" {
		t.Fatalf("outcome %q pending %+v", res.Outcome, res.Pending)
	}
	if spy.runs.Load() != 0 || res.ToolCalls != 0 {
		t.Fatal("the tool must not run before approval")
	}
	if !strings.Contains(res.Steps[0].Observation, "publishing needs a human") {
		t.Fatalf("the reason should be in the trace: %q", res.Steps[0].Observation)
	}
}

func TestDeciderStopHaltsTheRun(t *testing.T) {
	spy := &spyTool{name: "spy"}
	m := llm.NewMock(`{"tool":"spy","args":{"n":1}}`, `{"final":"never reached"}`)
	stop := agent.DeciderFunc(func(context.Context, agent.Call) agent.Decision {
		return agent.Decision{Action: agent.Stop, Reason: "budget exceeded"}
	})
	a := newAgent(t, m, stop, spy)

	res, _ := a.Run(context.Background(), "go")
	if res.Outcome != agent.Stopped || spy.runs.Load() != 0 {
		t.Fatalf("outcome %q, tool runs %d", res.Outcome, spy.runs.Load())
	}
	if len(m.Requests()) != 1 {
		t.Fatalf("the model must not be called again after a stop, got %d calls", len(m.Requests()))
	}
}

func TestUnknownActionFailsClosed(t *testing.T) {
	spy := &spyTool{name: "spy"}
	m := llm.NewMock(`{"tool":"spy","args":{"n":1}}`)
	weird := agent.DeciderFunc(func(context.Context, agent.Call) agent.Decision {
		return agent.Decision{Action: agent.Action(99)}
	})
	a := newAgent(t, m, weird, spy)

	res, _ := a.Run(context.Background(), "go")
	if res.Outcome != agent.Stopped || spy.runs.Load() != 0 {
		t.Fatalf("an unknown action must stop the run, outcome %q", res.Outcome)
	}
}

func TestStepLimitStopsAnEndlessModel(t *testing.T) {
	spy := &spyTool{name: "spy"}
	script := make([]string, 20)
	for i := range script {
		script[i] = `{"tool":"spy","args":{"n":1}}`
	}
	m := llm.NewMock(script...)
	a := newAgent(t, m, agent.AlwaysAct{}, spy) // MaxSteps is 5

	res, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != agent.StepLimit || res.ToolCalls != 5 {
		t.Fatalf("outcome %q, tool calls %d, want step_limit and 5", res.Outcome, res.ToolCalls)
	}
}

func TestReplyParsing(t *testing.T) {
	cases := map[string]string{
		"code fence":     "```json\n{\"final\":\"done\"}\n```",
		"sentence first": "Sure, here you go: {\"final\":\"done\"} hope that helps",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			a := newAgent(t, llm.NewMock(content), agent.AlwaysAct{})
			res, err := a.Run(context.Background(), "go")
			if err != nil || res.Outcome != agent.Finished || res.Final != "done" {
				t.Fatalf("outcome %q final %q err %v", res.Outcome, res.Final, err)
			}
		})
	}
}

func TestBadRepliesAreObservationsThenTheModelRecovers(t *testing.T) {
	bad := []string{
		"I will count the words now.", // no JSON
		`{"tool": "count_words", `,    // truncated JSON
		`{"hello": "world"}`,          // neither tool nor final
		`{"tool":"x","final":"y"}`,    // both
	}
	m := llm.NewMock(append(bad, `{"final":"ok"}`)...)
	a, _ := agent.New(m, nil, agent.AlwaysAct{}, agent.Config{MaxSteps: 6})

	res, err := a.Run(context.Background(), "go")
	if err != nil || res.Outcome != agent.Finished {
		t.Fatalf("outcome %q err %v", res.Outcome, err)
	}
	if len(res.Steps) != 4 {
		t.Fatalf("want 4 invalid reply steps, got %d", len(res.Steps))
	}
	for _, s := range res.Steps {
		if s.Call != nil || !strings.HasPrefix(s.Observation, "invalid reply") {
			t.Fatalf("unexpected step %+v", s)
		}
	}
}

func TestProviderErrorReturnsThePartialResult(t *testing.T) {
	m := llm.NewMock(`{"tool":"count_words","args":{"text":"a b"}}`)
	boom := errors.New("provider down")
	a := newAgent(t, m, agent.AlwaysAct{}, tools.CountWords{})

	// First call works, second hits the empty script.
	res, err := a.Run(context.Background(), "go")
	if !errors.Is(err, llm.ErrScriptExhausted) {
		t.Fatalf("want ErrScriptExhausted, got %v", err)
	}
	if res == nil || res.ToolCalls != 1 || len(res.Steps) != 1 {
		t.Fatalf("the partial result must survive, got %+v", res)
	}

	m2 := llm.NewMock(`{"final":"x"}`)
	m2.FailNext(boom)
	a2 := newAgent(t, m2, agent.AlwaysAct{})
	if _, err := a2.Run(context.Background(), "go"); !errors.Is(err, boom) {
		t.Fatalf("want the provider error wrapped, got %v", err)
	}
}

func TestCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a := newAgent(t, llm.NewMock(`{"final":"x"}`), agent.AlwaysAct{})
	if _, err := a.Run(ctx, "go"); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestNewRejectsUnsafeConfig(t *testing.T) {
	m := llm.NewMock()
	if _, err := agent.New(m, nil, nil, agent.Config{}); err == nil {
		t.Fatal("a missing decider must be rejected, there is no implicit permission")
	}
	if _, err := agent.New(nil, nil, agent.AlwaysAct{}, agent.Config{}); err == nil {
		t.Fatal("a missing provider must be rejected")
	}
	dup := []agent.Tool{tools.CountWords{}, tools.CountWords{}}
	if _, err := agent.New(m, dup, agent.AlwaysAct{}, agent.Config{}); err == nil {
		t.Fatal("duplicate tool names must be rejected")
	}
}

func TestSystemPromptListsToolsAndFormat(t *testing.T) {
	m := llm.NewMock(`{"final":"x"}`)
	a := newAgent(t, m, agent.AlwaysAct{}, tools.All()...)
	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	sys := m.Requests()[0].Messages[0]
	if sys.Role != llm.RoleSystem {
		t.Fatalf("first message role = %q", sys.Role)
	}
	for _, want := range []string{"count_words", "check_repetition", "get_policy", `{"final"`, "duration_minutes (integer)"} {
		if !strings.Contains(sys.Content, want) {
			t.Fatalf("system prompt is missing %q:\n%s", want, sys.Content)
		}
	}
}
