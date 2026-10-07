// Command demo runs the Day 3 agent on a small task: check whether a draft
// script is long enough and not repetitive.
//
// Without an API key it uses a scripted mock, so the loop is visible and
// repeatable. Set GROQ_API_KEY to run the same task with a real model.
// GROQ_MODEL is required with a key, there is no default model.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"sleepy-agent/internal/agent"
	"sleepy-agent/internal/llm"
	"sleepy-agent/internal/tools"
)

const draft = "Settle in. Let your shoulders soften. Somewhere beyond the hedge, the night air moves slowly " +
	"through the leaves, and every star above you is perfectly still."

func main() {
	provider, mode := chooseProvider()

	a, err := agent.New(provider, tools.All(), agent.AlwaysAct{}, agent.Config{MaxSteps: 8})
	if err != nil {
		fmt.Fprintln(os.Stderr, "setup failed:", err)
		os.Exit(1)
	}

	task := "Check this draft script for a 5 minute sleep episode. Use the tools to measure it against the policy " +
		"and for repetition, then give a short verdict.\n\nDRAFT:\n" + draft

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	res, err := a.Run(ctx, task)
	fmt.Printf("provider  : %s\n", provider.Name())
	fmt.Printf("mode      : %s\n\n", mode)
	if res != nil {
		printTrace(res)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "\nrun failed:", err)
		os.Exit(1)
	}
}

func chooseProvider() (llm.Provider, string) {
	if key := os.Getenv("GROQ_API_KEY"); key != "" {
		model := os.Getenv("GROQ_MODEL")
		if model == "" {
			fmt.Fprintln(os.Stderr, "GROQ_MODEL is required when GROQ_API_KEY is set (see https://api.groq.com/openai/v1/models)")
			os.Exit(2)
		}
		return llm.NewGroq(key, model), "real model"
	}
	return scriptedMock(), "scripted mock (canned answers, not a real model)"
}

// scriptedMock plays the three calls a sensible model would make, then a
// verdict computed from the same numbers the tools return.
func scriptedMock() llm.Provider {
	call := func(tool string, args map[string]any) string {
		b, _ := json.Marshal(map[string]any{"tool": tool, "args": args})
		return string(b)
	}
	words := len(strings.Fields(draft))
	verdict, _ := json.Marshal(map[string]string{
		"final": fmt.Sprintf("Too short: %d words, the minimum for 5 minutes is 500. No repetition problem.", words),
	})
	return llm.NewMock(
		call("get_policy", map[string]any{"duration_minutes": 5}),
		call("count_words", map[string]any{"text": draft}),
		call("check_repetition", map[string]any{"text": draft}),
		string(verdict),
	)
}

func printTrace(res *agent.Result) {
	for _, s := range res.Steps {
		if s.Call == nil {
			fmt.Printf("step %d  invalid reply -> %s\n", s.Index, s.Observation)
			continue
		}
		decision := "n/a"
		if s.Decision != nil {
			decision = s.Decision.Action.String()
		}
		fmt.Printf("step %d  %s (decision: %s, ran: %v)\n        -> %s\n", s.Index, s.Call.Tool, decision, s.Executed, s.Observation)
	}
	fmt.Printf("\noutcome   : %s\n", res.Outcome)
	if res.Final != "" {
		fmt.Printf("verdict   : %s\n", res.Final)
	}
	fmt.Printf("tool calls: %d\n", res.ToolCalls)
	fmt.Printf("tokens    : %d prompt, %d completion\n", res.Usage.PromptTokens, res.Usage.CompletionTokens)
}
