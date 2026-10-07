// Package tools holds the deterministic tools the agent can call.
//
// They measure things the model is bad at counting: words, repetition,
// limits. They are plain functions of their input, so tests never need a model.
package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"sleepy-agent/internal/agent"
	"sleepy-agent/internal/policy"
)

// All returns the built in tools.
func All() []agent.Tool {
	return []agent.Tool{CountWords{}, CheckRepetition{}, GetPolicy{}}
}

// CountWords counts the words in a text.
type CountWords struct{}

func (CountWords) Name() string        { return "count_words" }
func (CountWords) Description() string { return "Count the words in a text." }
func (CountWords) Schema() agent.Schema {
	return agent.Schema{
		Properties: map[string]agent.Prop{"text": {Type: "string", Description: "the text to measure"}},
		Required:   []string{"text"},
	}
}

func (CountWords) Run(_ context.Context, args map[string]any) (string, error) {
	return fmt.Sprintf("%d", len(strings.Fields(args["text"].(string)))), nil
}

// CheckRepetition reports how repetitive a text is.
type CheckRepetition struct{}

func (CheckRepetition) Name() string { return "check_repetition" }
func (CheckRepetition) Description() string {
	return "Measure repetition in a text: how many sentences are unique, and how much of the text the most common long word takes."
}
func (CheckRepetition) Schema() agent.Schema {
	return agent.Schema{
		Properties: map[string]agent.Prop{"text": {Type: "string", Description: "the text to measure"}},
		Required:   []string{"text"},
	}
}

func (CheckRepetition) Run(_ context.Context, args map[string]any) (string, error) {
	r := Repetition(args["text"].(string))
	return fmt.Sprintf("sentences=%d unique_sentences=%d unique_ratio=%.2f top_word=%q top_word_share=%.2f",
		r.Sentences, r.UniqueSentences, r.UniqueRatio, r.TopWord, r.TopWordShare), nil
}

// RepetitionReport is the measurement behind check_repetition. Day 7 reuses it
// for the QA gate.
type RepetitionReport struct {
	Sentences       int
	UniqueSentences int
	UniqueRatio     float64 // unique sentences divided by sentences, 1 means no repeats
	TopWord         string  // most common word of 4 or more letters
	TopWordShare    float64 // that word's share of all words
}

// Repetition measures a text.
func Repetition(text string) RepetitionReport {
	sentences := splitSentences(text)
	seen := make(map[string]struct{}, len(sentences))
	for _, s := range sentences {
		seen[strings.ToLower(s)] = struct{}{}
	}

	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	counts := map[string]int{}
	for _, w := range words {
		if len([]rune(w)) >= 4 {
			counts[w]++
		}
	}
	top, topN := "", 0
	keys := make([]string, 0, len(counts))
	for w := range counts {
		keys = append(keys, w)
	}
	sort.Strings(keys) // stable tie break so the output is deterministic
	for _, w := range keys {
		if counts[w] > topN {
			top, topN = w, counts[w]
		}
	}

	rep := RepetitionReport{Sentences: len(sentences), UniqueSentences: len(seen), TopWord: top}
	if len(sentences) > 0 {
		rep.UniqueRatio = float64(len(seen)) / float64(len(sentences))
	}
	if len(words) > 0 {
		rep.TopWordShare = float64(topN) / float64(len(words))
	}
	return rep
}

func splitSentences(text string) []string {
	parts := strings.FieldsFunc(text, func(r rune) bool {
		return r == '.' || r == '!' || r == '?' || r == '\n'
	})
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// GetPolicy returns the word limits for an episode length.
type GetPolicy struct{}

func (GetPolicy) Name() string { return "get_policy" }
func (GetPolicy) Description() string {
	return "Get the minimum, maximum and target word counts for an episode. Defaults to 5 minutes."
}
func (GetPolicy) Schema() agent.Schema {
	return agent.Schema{
		Properties: map[string]agent.Prop{"duration_minutes": {Type: "integer", Description: "episode length in minutes, 1 to 60"}},
	}
}

func (GetPolicy) Run(_ context.Context, args map[string]any) (string, error) {
	minutes := 5
	if v, ok := args["duration_minutes"]; ok {
		minutes = int(v.(float64))
	}
	if minutes < 1 || minutes > 60 {
		return "", fmt.Errorf("duration_minutes must be between 1 and 60, got %d", minutes)
	}
	p := policy.ForDuration(minutes)
	return fmt.Sprintf("min_words=%d max_words=%d target_words=%d", p.MinWords, p.MaxWords, p.TargetWords), nil
}
