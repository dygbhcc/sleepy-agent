package llm

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestLoggedWritesPromptAndReplyAndChangesNothing(t *testing.T) {
	var buf bytes.Buffer
	l := &Logged{Inner: NewMock("the reply"), W: &buf}
	resp, err := l.Complete(context.Background(), Request{
		Messages:    []Message{{Role: RoleSystem, Content: "sys text"}, {Role: RoleUser, Content: "user text"}},
		Temperature: 0.7, MaxTokens: 1500,
	})
	if err != nil || resp.Content != "the reply" {
		t.Fatalf("got %q, %v", resp.Content, err)
	}
	got := buf.String()
	for _, want := range []string{"call 1", "temperature 0.70", "max_tokens 1500", "sys text", "user text", "the reply"} {
		if !strings.Contains(got, want) {
			t.Errorf("log lacks %q:\n%s", want, got)
		}
	}
	if l.Name() != NewMock().Name() {
		t.Error("Name must come from the wrapped provider")
	}
}

func TestLoggedNumbersCallsAndLogsErrors(t *testing.T) {
	var buf bytes.Buffer
	l := &Logged{Inner: failing{}, W: &buf}
	for i := 0; i < 2; i++ {
		if _, err := l.Complete(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "q"}}}); err == nil {
			t.Fatal("the error must pass through")
		}
	}
	got := buf.String()
	if !strings.Contains(got, "call 2") || strings.Count(got, "boom") != 2 {
		t.Fatalf("want two numbered calls with their errors:\n%s", got)
	}
}

type failing struct{}

func (failing) Name() string { return "failing" }
func (failing) Complete(context.Context, Request) (Response, error) {
	return Response{}, errors.New("boom")
}
