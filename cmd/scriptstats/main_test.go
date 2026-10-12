package main

import (
	"context"
	"testing"

	"sleepy-agent/internal/policy"
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
}
