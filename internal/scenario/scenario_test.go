package scenario_test

import (
	"context"
	"testing"

	"sleepy-agent/internal/agent"
	"sleepy-agent/internal/llm"
	"sleepy-agent/internal/scenario"
	"sleepy-agent/internal/tools"
)

func run(t *testing.T, p llm.Provider) *agent.Result {
	t.Helper()
	a, err := agent.New(p, tools.All(), agent.AlwaysAct{}, agent.Config{MaxSteps: 8})
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.Run(context.Background(), scenario.Task)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// Both adapters must produce the same run for the same model behaviour. This
// is the property that makes the comparison in cmd/compare meaningful.
func TestBothAdaptersGiveTheSameTrace(t *testing.T) {
	prompt, err := scenario.Adapt(scenario.ModePrompt, scenario.ScriptedPrompt())
	if err != nil {
		t.Fatal(err)
	}
	native, err := scenario.Adapt(scenario.ModeNative, scenario.ScriptedNative())
	if err != nil {
		t.Fatal(err)
	}
	rp, rn := run(t, prompt), run(t, native)

	for name, r := range map[string]*agent.Result{"prompt": rp, "native": rn} {
		if !scenario.Completed(r) || r.ToolCalls != 3 {
			t.Fatalf("%s: not completed, outcome %q, tool calls %d", name, r.Outcome, r.ToolCalls)
		}
	}
	if rp.Final != rn.Final || len(rp.Steps) != len(rn.Steps) {
		t.Fatalf("traces differ:\nprompt %+v\nnative %+v", rp, rn)
	}
	for i := range rp.Steps {
		if rp.Steps[i].Observation != rn.Steps[i].Observation {
			t.Fatalf("step %d observation differs: %q vs %q", i+1, rp.Steps[i].Observation, rn.Steps[i].Observation)
		}
	}
}

func TestCompletedRequiresEveryToolToRun(t *testing.T) {
	// A model that answers without using the tools did not do the task.
	res := run(t, llm.NewPromptTools(llm.NewMock(`{"final":"looks fine"}`)))
	if res.Outcome != agent.Finished {
		t.Fatalf("setup: outcome %q", res.Outcome)
	}
	if scenario.Completed(res) {
		t.Fatal("finishing without running the tools must not count as completed")
	}
	if scenario.Completed(nil) {
		t.Fatal("nil result must not count as completed")
	}
}

func TestAdaptRejectsUnknownMode(t *testing.T) {
	if _, err := scenario.Adapt("telepathy", llm.NewMock()); err == nil {
		t.Fatal("an unknown mode must be rejected")
	}
}
