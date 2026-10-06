// Package llm defines the single seam between the agent and any language model.
//
// Everything above this package talks to a Provider. Everything below it
// (mock, Groq, OpenAI) is swappable. That is what lets the whole repo run
// without an API key and keeps tests deterministic.
package llm

import "context"

// Role of a chat message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one turn in a conversation.
type Message struct {
	Role    Role
	Content string
}

// Request is what the agent asks a model to do.
type Request struct {
	Messages    []Message
	Temperature float64
	MaxTokens   int
}

// Usage is the token accounting for one call. Cost guardrails (Day 13)
// are built on top of this.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
}

// Response is what a model answered.
type Response struct {
	Content string
	Usage   Usage
}

// Provider is the only interface the rest of the system depends on.
type Provider interface {
	// Name identifies the provider in logs and traces.
	Name() string
	// Complete runs one model call. It must honour ctx cancellation.
	Complete(ctx context.Context, req Request) (Response, error)
}
