// Package llm defines the single seam between the agent and any language model.
//
// Everything above this package talks to a Provider. Everything below it
// (mock, Groq, OpenAI) is swappable. That is what lets the whole repo run
// without an API key and keeps tests deterministic.
package llm

import (
	"context"
	"encoding/json"
	"errors"
)

// Role of a chat message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	// RoleTool carries the result of a tool call back to the model.
	RoleTool Role = "tool"
)

// ErrMalformedReply means the model answered, but not in a form the adapter
// can read (broken JSON, a tool call with unreadable arguments). The Response
// returned next to it still carries Content and Usage, so cost is never lost.
var ErrMalformedReply = errors.New("llm: malformed model reply")

// ToolSpec describes one tool the model may call. Parameters is a JSON Schema
// object, the format native tool calling APIs expect.
type ToolSpec struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// ToolCall is a model's request to run one tool. Both adapters (native and
// prompt based) return calls in this one shape, so the agent never knows
// which kind of model it is talking to.
type ToolCall struct {
	ID   string // set by native APIs, may be empty
	Name string
	Args map[string]any
}

// Message is one turn in a conversation.
type Message struct {
	Role    Role
	Content string
	// ToolCalls is set on assistant messages that asked for a tool.
	ToolCalls []ToolCall
	// ToolCallID is set on RoleTool messages and names the call they answer.
	ToolCallID string
}

// Request is what the agent asks a model to do.
type Request struct {
	Messages    []Message
	Temperature float64
	MaxTokens   int
	// JSON asks the provider to constrain the reply to a single JSON object.
	// Providers that cannot do this ignore it.
	JSON bool
	// Tools lists the tools the model may call. When empty the model just
	// answers in text.
	Tools []ToolSpec
}

// Usage is the token accounting for one call. Cost guardrails (Day 9)
// are built on top of this.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
}

// Response is what a model answered.
//
// The contract every Provider follows: if ToolCalls is not empty the model
// wants a tool run, otherwise Content is its final answer.
type Response struct {
	Content   string
	ToolCalls []ToolCall
	Usage     Usage
}

// Provider is the only interface the rest of the system depends on.
type Provider interface {
	// Name identifies the provider in logs and traces.
	Name() string
	// Complete runs one model call. It must honour ctx cancellation.
	Complete(ctx context.Context, req Request) (Response, error)
}
