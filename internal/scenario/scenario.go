// Package scenario holds the one small task that cmd/demo and cmd/compare
// both run: check a draft sleep script against the policy and for repetition.
//
// Keeping it in one place guarantees the demo and the comparison measure the
// same thing.
package scenario

import (
	"encoding/json"
	"fmt"
	"strings"

	"sleepy-agent/internal/agent"
	"sleepy-agent/internal/llm"
)

// Draft is deliberately too short for a 5 minute episode.
const Draft = "Settle in. Let your shoulders soften. Somewhere beyond the hedge, the night air moves slowly " +
	"through the leaves, and every star above you is perfectly still."

// Task is what the agent is asked to do.
const Task = "Check this draft script for a 5 minute sleep episode. Use the tools to measure it against the policy " +
	"and for repetition, then give a short verdict.\n\nDRAFT:\n" + Draft

// RequiredTools are the tools a model must actually run to have done the task.
var RequiredTools = []string{"get_policy", "count_words", "check_repetition"}

// Modes are the two ways a model can be driven.
const (
	ModePrompt = "prompt" // tools described in the prompt, JSON reply parsed by us
	ModeNative = "native" // the provider's own tool calling API
)

// Adapt returns p driven in the given mode. Native passes p through, because
// OpenAICompat already speaks native tool calling.
func Adapt(mode string, p llm.Provider) (llm.Provider, error) {
	switch mode {
	case ModePrompt:
		return llm.NewPromptTools(p), nil
	case ModeNative:
		return p, nil
	default:
		return nil, fmt.Errorf("unknown mode %q, want %q or %q", mode, ModePrompt, ModeNative)
	}
}

// Completed reports whether a run really did the task: it finished, and every
// required tool ran. A model that skips the tools and guesses a verdict is
// not counted as success.
func Completed(res *agent.Result) bool {
	if res == nil || res.Outcome != agent.Finished {
		return false
	}
	ran := map[string]bool{}
	for _, s := range res.Steps {
		if s.Call != nil && s.Executed {
			ran[s.Call.Tool] = true
		}
	}
	for _, name := range RequiredTools {
		if !ran[name] {
			return false
		}
	}
	return true
}

// InvalidReplies counts steps where the model's reply was unusable.
func InvalidReplies(res *agent.Result) int {
	n := 0
	for _, s := range res.Steps {
		if s.Call == nil {
			n++
		}
	}
	return n
}

// ScriptedPrompt plays the four replies a sensible model would give, as JSON
// text. Wrap it with Adapt(ModePrompt, ...).
func ScriptedPrompt() *llm.Mock {
	return llm.NewMock(
		jsonCall("get_policy", map[string]any{"duration_minutes": 5}),
		jsonCall("count_words", map[string]any{"text": Draft}),
		jsonCall("check_repetition", map[string]any{"text": Draft}),
		fmt.Sprintf(`{"final": %q}`, verdict()),
	)
}

// ScriptedNative plays the same four replies as native tool calls. Use it
// directly, without an adapter.
func ScriptedNative() *llm.Mock {
	call := func(id, name string, args map[string]any) llm.Response {
		return llm.Response{ToolCalls: []llm.ToolCall{{ID: id, Name: name, Args: args}}}
	}
	return llm.NewMockResponses(
		call("c1", "get_policy", map[string]any{"duration_minutes": float64(5)}), // JSON numbers decode as float64
		call("c2", "count_words", map[string]any{"text": Draft}),
		call("c3", "check_repetition", map[string]any{"text": Draft}),
		llm.Response{Content: verdict()},
	)
}

func verdict() string {
	return fmt.Sprintf("Too short: %d words, the minimum for 5 minutes is 500. No repetition problem.", len(strings.Fields(Draft)))
}

func jsonCall(tool string, args map[string]any) string {
	return fmt.Sprintf(`{"tool": %q, "args": %s}`, tool, mustJSON(args))
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
