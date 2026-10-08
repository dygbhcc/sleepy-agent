package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
)

var countSpec = ToolSpec{
	Name:        "count_words",
	Description: "counts words",
	Parameters:  []byte(`{"type":"object","properties":{"text":{"type":"string","description":"the text"}},"required":["text"]}`),
}

func TestPromptToolsParsesACallAndAFinalAnswer(t *testing.T) {
	m := NewMock(`{"tool":"count_words","args":{"text":"a b"}}`, "```json\n{\"final\":\"done\"}\n```")
	p := NewPromptTools(m)

	resp, err := p.Complete(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "go"}}, Tools: []ToolSpec{countSpec}})
	if err != nil || len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "count_words" || resp.ToolCalls[0].Args["text"] != "a b" {
		t.Fatalf("call: %+v err %v", resp, err)
	}
	resp, err = p.Complete(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "go"}}})
	if err != nil || len(resp.ToolCalls) != 0 || resp.Content != "done" {
		t.Fatalf("final: %+v err %v", resp, err)
	}
}

func TestPromptToolsRewritesTheConversationForAModelWithoutToolSupport(t *testing.T) {
	m := NewMock(`{"final":"ok"}`)
	_, err := NewPromptTools(m).Complete(context.Background(), Request{
		Tools: []ToolSpec{countSpec},
		Messages: []Message{
			{Role: RoleSystem, Content: "be an agent"},
			{Role: RoleUser, Content: "task"},
			{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "x", Name: "count_words", Args: map[string]any{"text": "a"}}}},
			{Role: RoleTool, ToolCallID: "x", Content: "1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := m.Requests()[0]
	if !got.JSON || len(got.Tools) != 0 {
		t.Fatalf("the inner model must get JSON mode and no native tools: %+v", got)
	}
	if len(got.Messages) != 4 {
		t.Fatalf("want 4 messages, got %d", len(got.Messages))
	}
	sys := got.Messages[0].Content
	for _, want := range []string{"be an agent", `{"final"`, "count_words: counts words", "text (string): the text"} {
		if !strings.Contains(sys, want) {
			t.Fatalf("system prompt lacks %q:\n%s", want, sys)
		}
	}
	if got.Messages[2].Role != RoleAssistant || got.Messages[2].Content != `{"args":{"text":"a"},"tool":"count_words"}` {
		t.Fatalf("assistant call not rewritten as JSON: %+v", got.Messages[2])
	}
	if got.Messages[3].Role != RoleUser || got.Messages[3].Content != "Observation: 1" {
		t.Fatalf("tool result not rewritten as an observation: %+v", got.Messages[3])
	}
}

func TestPromptToolsAddsASystemMessageWhenThereIsNone(t *testing.T) {
	m := NewMock(`{"final":"ok"}`)
	_, _ = NewPromptTools(m).Complete(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "hi"}}})
	msgs := m.Requests()[0].Messages
	if len(msgs) != 2 || msgs[0].Role != RoleSystem {
		t.Fatalf("want a system message first, got %+v", msgs)
	}
}

func TestPromptToolsMalformedRepliesKeepUsageAndContent(t *testing.T) {
	bad := []string{"no json here", `{"tool": "count_words", `, `{"hello":"world"}`, `{"tool":"x","final":"y"}`}
	for _, content := range bad {
		resp, err := NewPromptTools(NewMock(content)).Complete(context.Background(), Request{})
		if !errors.Is(err, ErrMalformedReply) {
			t.Fatalf("%q: want ErrMalformedReply, got %v", content, err)
		}
		if resp.Content != content || resp.Usage.CompletionTokens == 0 {
			t.Fatalf("%q: content and usage must survive, got %+v", content, resp)
		}
	}
}

func TestPromptToolsPassesProviderErrorsThrough(t *testing.T) {
	m := NewMock()
	boom := errors.New("down")
	m.FailNext(boom)
	if _, err := NewPromptTools(m).Complete(context.Background(), Request{}); !errors.Is(err, boom) {
		t.Fatalf("want the provider error, got %v", err)
	}
}
