package pipeline

import (
	"context"
	"errors"
	"strings"
	"testing"

	"sleepy-agent/internal/llm"
)

func TestRunProducesEpisode(t *testing.T) {
	m := llm.NewMock("  Nebula Gardens  ", "Breathe in. The stars drift by.")

	ep, err := Run(context.Background(), m, "a quiet nebula")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ep.Title != "Nebula Gardens" {
		t.Fatalf("title = %q, want trimmed title", ep.Title)
	}
	if ep.Script != "Breathe in. The stars drift by." {
		t.Fatalf("script = %q", ep.Script)
	}
	if ep.Usage.CompletionTokens == 0 {
		t.Fatal("usage should be accumulated across both calls")
	}
}

func TestRunSendsTopicAndTitleToTheModel(t *testing.T) {
	m := llm.NewMock("Moon Lake", "script")
	if _, err := Run(context.Background(), m, "a moonlit lake"); err != nil {
		t.Fatal(err)
	}
	reqs := m.Requests()
	if len(reqs) != 2 {
		t.Fatalf("got %d model calls, want 2", len(reqs))
	}
	if !strings.Contains(reqs[0].Messages[1].Content, "a moonlit lake") {
		t.Fatal("title call must carry the topic")
	}
	if !strings.Contains(reqs[1].Messages[1].Content, "Moon Lake") {
		t.Fatal("script call must carry the generated title")
	}
}

func TestRunReportsWhichStageFailed(t *testing.T) {
	boom := errors.New("provider down")
	m := llm.NewMock("Title")
	// First call succeeds, second call runs out of script.
	_, err := Run(context.Background(), m, "topic")

	var se *StageError
	if !errors.As(err, &se) || se.Stage != "script" {
		t.Fatalf("got %v, want StageError for script", err)
	}
	if !errors.Is(err, llm.ErrScriptExhausted) {
		t.Fatalf("StageError must unwrap to the cause, got %v", err)
	}

	m2 := llm.NewMock("unused")
	m2.FailNext(boom)
	_, err = Run(context.Background(), m2, "topic")
	if !errors.As(err, &se) || se.Stage != "title" || !errors.Is(err, boom) {
		t.Fatalf("got %v, want StageError for title wrapping boom", err)
	}
}
