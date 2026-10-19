package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"sleepy-agent/internal/domain"
	"sleepy-agent/internal/llm"
	"sleepy-agent/internal/script"
)

func mockBuild() llm.Provider { return shortWriter{} }

func TestExperimentBlindNeverRecoversDirectedDoes(t *testing.T) {
	var out bytes.Buffer
	cfg := script.Config{Style: "Earthside", DurationMin: 5}
	rs, engine, err := experiment(context.Background(), &out, mockBuild, cfg, 3, 3, 0, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 6 {
		t.Fatalf("results = %d, want 6 (3 topics x 2 arms)", len(rs))
	}
	for _, r := range byArm(rs, "blind") {
		if r.status != domain.StatusNeedsReview || len(r.attempts) != 3 {
			t.Errorf("blind %s: status %s attempts %d, want NEEDS_REVIEW after 3", r.topic, r.status, len(r.attempts))
		}
		if r.attempts[1].kind != "same request" {
			t.Errorf("blind retry kind = %q", r.attempts[1].kind)
		}
	}
	for _, r := range byArm(rs, "directed") {
		if !r.accepted() || len(r.attempts) != 2 || r.attempts[1].kind != "continue" {
			t.Errorf("directed %s: %s %v, want continue then pass", r.topic, r.status, describe(r.attempts))
		}
	}
	if engine == nil || len(engine.Stats()) == 0 {
		t.Fatal("directed arm should expose plan stats")
	}
}

func TestReportTables(t *testing.T) {
	var out bytes.Buffer
	cfg := script.Config{Style: "Earthside", DurationMin: 5}
	rs, engine, err := experiment(context.Background(), &bytes.Buffer{}, mockBuild, cfg, 2, 3, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	report(&out, rs, engine, "scripted mock", 3)
	s := out.String()
	for _, want := range []string{"| blind | 2 | 0 | 2 | 0/2 |", "| directed | 2 | 0 | 2 | 2/2 |", "WORDCOUNT_LOW", "wc_low_continue", "noise"} {
		if !strings.Contains(s, want) {
			t.Errorf("report misses %q:\n%s", want, s)
		}
	}
}

func TestShortWriterIsSteeredOnlyByThePrompt(t *testing.T) {
	plain, _ := shortWriter{}.Complete(context.Background(), llm.Request{Messages: []llm.Message{{Content: "writer"}, {Content: "Write paragraphs"}}})
	if n := len(strings.Fields(plain.Content)); n > 20 {
		t.Errorf("unsteered output has %d words, want a short one", n)
	}
	steered, _ := shortWriter{}.Complete(context.Background(), llm.Request{Messages: []llm.Message{{Content: "writer"}, {Content: "REPAIR NOTE x"}}})
	if n := len(strings.Fields(steered.Content)); n < 500 {
		t.Errorf("steered output has %d words, want a full script", n)
	}
}

func TestOneLine(t *testing.T) {
	if got := oneLine("a\n  b"); got != "a b" {
		t.Errorf("got %q", got)
	}
	if got := oneLine(strings.Repeat("x", 300)); len(got) != 203 {
		t.Errorf("len = %d", len(got))
	}
}
