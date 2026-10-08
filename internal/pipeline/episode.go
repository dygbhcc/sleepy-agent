// Package pipeline turns a topic into an episode by calling a Provider.
//
// Day 2 keeps this deliberately naive: two sequential calls, no state,
// no retries. Day 4 moved orchestration into a state machine and Day 5
// replaces this generator with the ported one. The point of
// writing the naive version first is to feel exactly where it breaks.
package pipeline

import (
	"context"
	"fmt"
	"strings"

	"sleepy-agent/internal/llm"
)

// Episode is the result of one pipeline run.
type Episode struct {
	Topic  string
	Title  string
	Script string
	Usage  llm.Usage
}

// StageError says which stage failed, so logs and later retries can act on it.
type StageError struct {
	Stage string
	Err   error
}

func (e *StageError) Error() string { return fmt.Sprintf("stage %s: %v", e.Stage, e.Err) }
func (e *StageError) Unwrap() error { return e.Err }

// Run asks the provider for a title, then for a script that uses it.
func Run(ctx context.Context, p llm.Provider, topic string) (Episode, error) {
	ep := Episode{Topic: topic}

	titleResp, err := p.Complete(ctx, llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "You write calm, gentle titles for sleep narrations."},
			{Role: llm.RoleUser, Content: "Write one title for an episode about: " + topic},
		},
		Temperature: 0.7,
		MaxTokens:   32,
	})
	if err != nil {
		return ep, &StageError{Stage: "title", Err: err}
	}
	ep.Title = strings.TrimSpace(titleResp.Content)
	ep.Usage = add(ep.Usage, titleResp.Usage)

	scriptResp, err := p.Complete(ctx, llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "You write slow, soothing sleep narrations."},
			{Role: llm.RoleUser, Content: "Write the narration script for the episode titled: " + ep.Title},
		},
		Temperature: 0.7,
		MaxTokens:   1024,
	})
	if err != nil {
		return ep, &StageError{Stage: "script", Err: err}
	}
	ep.Script = strings.TrimSpace(scriptResp.Content)
	ep.Usage = add(ep.Usage, scriptResp.Usage)

	return ep, nil
}

func add(a, b llm.Usage) llm.Usage {
	return llm.Usage{
		PromptTokens:     a.PromptTokens + b.PromptTokens,
		CompletionTokens: a.CompletionTokens + b.CompletionTokens,
	}
}
