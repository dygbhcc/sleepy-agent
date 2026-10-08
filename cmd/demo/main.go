// Command demo runs the Day 3 agent on a small task: check whether a draft
// script is long enough and not repetitive.
//
// Without an API key it uses a scripted mock, so the loop is visible and
// repeatable. Set GROQ_API_KEY to run the same task with a real model.
// GROQ_MODEL is required with a key, there is no default model.
//
// AGENT_MODE picks how the model is driven: "prompt" (default, tools in the
// prompt, JSON reply) or "native" (the provider's tool calling API). Use
// `make compare` to measure both side by side.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"sleepy-agent/internal/agent"
	"sleepy-agent/internal/llm"
	"sleepy-agent/internal/scenario"
	"sleepy-agent/internal/tools"
)

func main() {
	mode := os.Getenv("AGENT_MODE")
	if mode == "" {
		mode = scenario.ModePrompt
	}
	provider, label := chooseProvider(mode)

	a, err := agent.New(provider, tools.All(), agent.AlwaysAct{}, agent.Config{MaxSteps: 8})
	if err != nil {
		fmt.Fprintln(os.Stderr, "setup failed:", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	res, err := a.Run(ctx, scenario.Task)
	fmt.Printf("provider  : %s\n", provider.Name())
	fmt.Printf("mode      : %s\n\n", label)
	if res != nil {
		printTrace(res)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "\nrun failed:", err)
		os.Exit(1)
	}
}

func chooseProvider(mode string) (llm.Provider, string) {
	if key := os.Getenv("GROQ_API_KEY"); key != "" {
		model := os.Getenv("GROQ_MODEL")
		if model == "" {
			fmt.Fprintln(os.Stderr, "GROQ_MODEL is required when GROQ_API_KEY is set (see https://api.groq.com/openai/v1/models)")
			os.Exit(2)
		}
		p, err := scenario.Adapt(mode, llm.NewGroq(key, model))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		return p, "real model, " + mode + " tool calling"
	}
	var mock llm.Provider = scenario.ScriptedNative()
	if mode == scenario.ModePrompt {
		mock = llm.NewPromptTools(scenario.ScriptedPrompt())
	}
	return mock, "scripted mock, " + mode + " tool calling (canned answers, not a real model)"
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
