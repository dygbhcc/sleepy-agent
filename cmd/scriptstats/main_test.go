package main

import (
	"context"
	"testing"

	"sleepy-agent/internal/policy"
	"sleepy-agent/internal/qa"
	"sleepy-agent/internal/script"
)

// The keyless harness must show both outcomes, otherwise it proves nothing.
func TestMockRunsShowACleanAndABrokenScript(t *testing.T) {
	g, _ := script.New(mockFor(1, nil), script.Config{})
	clean, err := g.Generate(context.Background(), "x")
	if err != nil || !script.Measure(clean.Body, policy.ForDuration(5)).Clean() {
		t.Fatalf("odd runs must be clean: %v", err)
	}
	g, _ = script.New(mockFor(2, nil), script.Config{})
	broken, _ := g.Generate(context.Background(), "x")
	if got := script.Measure(broken.Body, policy.ForDuration(5)).Violations; len(got) != 2 {
		t.Fatalf("even runs must break two rules, got %v", got)
	}
	// The production gate agrees on both: a clean script passes it (so the
	// sample is not repetitive enough to trip the repetition check), the broken
	// one is stopped by its banned word.
	if r := qa.Script(clean.Title, clean.Body, policy.ForDuration(5)); !r.Pass {
		t.Fatalf("a clean script must pass the gate: %s", r.Summary())
	}
	if r := qa.Script(broken.Title, broken.Body, policy.ForDuration(5)); r.Pass || r.FailType != qa.FailBannedPhrase {
		t.Fatalf("the broken script must be stopped by its banned word: %s", r.Summary())
	}
}

func TestConfigCarriesTheTokenBudgetAndKeepsTheDefaultWhenUnset(t *testing.T) {
	c := config("Cosmos", 7, 4000)
	if c.Style != "Cosmos" || c.DurationMin != 7 || c.MaxTokensPerChunk != 4000 {
		t.Fatalf("config = %+v", c)
	}
	if got := config("Cosmos", 7, 0).MaxTokensPerChunk; got != 0 {
		t.Fatalf("unset must stay zero so the generator default applies, got %d", got)
	}
}
