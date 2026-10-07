package tools

import (
	"context"
	"strings"
	"testing"
)

func run(t *testing.T, tool interface {
	Run(context.Context, map[string]any) (string, error)
}, args map[string]any) (string, error) {
	t.Helper()
	return tool.Run(context.Background(), args)
}

func TestCountWords(t *testing.T) {
	got, err := run(t, CountWords{}, map[string]any{"text": "settle in  and breathe\nslowly"})
	if err != nil || got != "5" {
		t.Fatalf("got %q, %v, want 5", got, err)
	}
	if got, _ := run(t, CountWords{}, map[string]any{"text": ""}); got != "0" {
		t.Fatalf("empty text should have 0 words, got %q", got)
	}
}

func TestRepetitionFlagsLoops(t *testing.T) {
	loop := strings.Repeat("The night is calm. ", 60)
	r := Repetition(loop)
	if r.Sentences != 60 || r.UniqueSentences != 1 {
		t.Fatalf("got %d sentences, %d unique, want 60 and 1", r.Sentences, r.UniqueSentences)
	}
	if r.UniqueRatio > 0.05 {
		t.Fatalf("a loop should have a tiny unique ratio, got %.2f", r.UniqueRatio)
	}
	if r.TopWord != "calm" && r.TopWord != "night" {
		t.Fatalf("unexpected top word %q", r.TopWord)
	}
}

func TestRepetitionOnVariedText(t *testing.T) {
	r := Repetition("The garden is quiet. Stars drift above the hedge. A soft wind moves the leaves.")
	if r.Sentences != 3 || r.UniqueSentences != 3 || r.UniqueRatio != 1 {
		t.Fatalf("varied text should have no repeated sentences: %+v", r)
	}
}

func TestRepetitionEmpty(t *testing.T) {
	r := Repetition("  ")
	if r.Sentences != 0 || r.UniqueRatio != 0 || r.TopWordShare != 0 {
		t.Fatalf("empty text should report zeros: %+v", r)
	}
}

func TestRepetitionTieBreakIsDeterministic(t *testing.T) {
	first := Repetition("alpha beta gamma delta").TopWord
	for i := 0; i < 20; i++ {
		if got := Repetition("alpha beta gamma delta").TopWord; got != first {
			t.Fatalf("top word changed between runs: %q then %q", first, got)
		}
	}
}

func TestGetPolicy(t *testing.T) {
	got, err := run(t, GetPolicy{}, map[string]any{})
	if err != nil || got != "min_words=500 max_words=800 target_words=650" {
		t.Fatalf("default policy wrong: %q, %v", got, err)
	}
	got, _ = run(t, GetPolicy{}, map[string]any{"duration_minutes": float64(10)})
	if got != "min_words=1000 max_words=1600 target_words=1300" {
		t.Fatalf("10 minute policy wrong: %q", got)
	}
	if _, err := run(t, GetPolicy{}, map[string]any{"duration_minutes": float64(0)}); err == nil {
		t.Fatal("0 minutes should be rejected")
	}
}

func TestAllToolsHaveUniqueNames(t *testing.T) {
	seen := map[string]bool{}
	for _, tool := range All() {
		if seen[tool.Name()] {
			t.Fatalf("duplicate tool name %q", tool.Name())
		}
		seen[tool.Name()] = true
	}
	if len(seen) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(seen))
	}
}
