package pipeline

import (
	"context"
	"strings"
	"testing"

	"sleepy-agent/internal/llm"
)

// These tests pin a known weakness. They do not endorse it.
//
// Sleepy's first real failures were scripts that were too short or that kept
// repeating the same words, and they were only caught by reading the output.
// The naive pipeline has no way to tell, so it accepts both. Day 7 adds QA
// gates, and these tests then flip to expect a rejection.

// sleepyMinWords is Sleepy's minimum for a 5 minute episode (100 words per minute).
const sleepyMinWords = 500

func TestKnownGapAcceptsTooShortScript(t *testing.T) {
	short := "Close your eyes. Breathe slowly."
	m := llm.NewMock("Quiet Night", short)

	ep, err := Run(context.Background(), m, "a quiet night")
	if err != nil {
		t.Fatalf("the naive pipeline is expected to accept this script, got: %v", err)
	}

	words := len(strings.Fields(ep.Script))
	if words >= sleepyMinWords {
		t.Fatalf("test setup is wrong: script has %d words, want fewer than %d", words, sleepyMinWords)
	}
	t.Logf("KNOWN GAP: accepted a %d word script, the minimum for a 5 minute episode is %d", words, sleepyMinWords)
}

func TestKnownGapAcceptsRepeatedSentence(t *testing.T) {
	sentence := "The night is calm and the stars drift slowly by."
	repeated := strings.TrimSpace(strings.Repeat(sentence+" ", 60))
	m := llm.NewMock("Calm Stars", repeated)

	ep, err := Run(context.Background(), m, "calm stars")
	if err != nil {
		t.Fatalf("the naive pipeline is expected to accept this script, got: %v", err)
	}

	sentences := strings.Split(strings.TrimSuffix(ep.Script, "."), ". ")
	unique := map[string]struct{}{}
	for _, s := range sentences {
		unique[s] = struct{}{}
	}
	if len(unique) != 1 {
		t.Fatalf("test setup is wrong: want one unique sentence, got %d", len(unique))
	}
	t.Logf("KNOWN GAP: accepted a script of %d sentences with only %d unique", len(sentences), len(unique))
}
