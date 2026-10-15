// Package qa holds the quality gates: deterministic checks that decide whether
// the output of a step may move on. The rules live in code, never in a prompt.
//
// Ported from Sleepy (internal/jobs/qa.go and internal/providers/llm/qa.go),
// reduced to what this repo has today: the script gate. The voice, render and
// package gates arrive with their real adapters on Day 10.
//
// A gate does not repair anything. It says pass or fail and why. What to do
// about a failure (retry, fix, ask a human) is the caller's decision; Day 7
// adds the fix engine.
//
// Two things differ from script.Measure on purpose. Measure counts how the
// model followed the prompt, with the prompt's strict rules (zero exclamation
// marks, every sentence 8 to 20 words). The gate holds production thresholds,
// the ones Sleepy's autopilot uses: a script with one exclamation mark is
// measured as a violation but does not fail the gate.
package qa

import (
	"fmt"
	"strings"
	"unicode"

	"sleepy-agent/internal/policy"
	"sleepy-agent/internal/script"
)

// StageScript names the script gate in reports.
const StageScript = "SCRIPTED"

// FailType says why a gate failed. The names are Sleepy's.
type FailType string

const (
	FailBannedPhrase  FailType = "BANNED_PHRASE"
	FailWordcountLow  FailType = "WORDCOUNT_LOW"
	FailWordcountHigh FailType = "WORDCOUNT_HIGH"
	FailPacing        FailType = "PACING_FAIL"
	FailTitleInvalid  FailType = "TITLE_INVALID"
)

// Check is one pass or fail assertion inside a report.
type Check struct {
	Name    string         `json:"name"`
	Pass    bool           `json:"pass"`
	Details string         `json:"details"`
	Diag    map[string]any `json:"diag,omitempty"` // numbers behind the verdict, for the fix engine
}

// Report is the result of a gate. FailType is the first thing that failed, the
// most specific reason, and Checks lists everything that was looked at.
type Report struct {
	Stage    string   `json:"stage"`
	Pass     bool     `json:"pass"`
	FailType FailType `json:"fail_type,omitempty"`
	Checks   []Check  `json:"checks"`
}

// Summary is a one line reason, for logs and for the human who reviews a run.
func (r Report) Summary() string {
	if r.Pass {
		return "passed"
	}
	var first string
	failed := 0
	for _, c := range r.Checks {
		if !c.Pass {
			if failed == 0 {
				first = c.Details
			}
			failed++
		}
	}
	s := fmt.Sprintf("%s: %s", r.FailType, first)
	if failed > 1 {
		s += fmt.Sprintf(" (and %d more)", failed-1)
	}
	return s
}

func (r *Report) pass(c Check) {
	c.Pass = true
	r.Checks = append(r.Checks, c)
}

// fail records a failed check. The first failure decides FailType, so a later,
// less specific check never hides the reason that mattered first.
func (r *Report) fail(ft FailType, c Check) {
	c.Pass = false
	r.Pass = false
	if r.FailType == "" {
		r.FailType = ft
	}
	r.Checks = append(r.Checks, c)
}

// Sleepy's thresholds for the sleep safety checks.
const (
	maxBannedReports   = 3    // list at most this many banned phrases, to keep reports short
	maxExclamations    = 2    // more than this fails
	maxAllCapsWords    = 3    // more than this fails
	maxAvgSentenceLen  = 25.0 // average words per sentence
	minUniqueWordRatio = 0.12 // unique words / all words, checked above 50 words
	minTitleLen        = 5
	maxTitleLen        = 40
)

// Script is the script gate: word count against the policy, banned phrases,
// the sleep safety checks, and the title.
func Script(title, body string, p policy.Policy) Report {
	r := Report{Stage: StageScript, Pass: true}

	// Word count.
	words := len(strings.Fields(body))
	diag := map[string]any{
		"actual_words": words, "min_words": p.MinWords, "max_words": p.MaxWords,
		"target_words": p.TargetWords, "delta_words": words - p.TargetWords,
	}
	switch {
	case words < p.MinWords:
		r.fail(FailWordcountLow, Check{Name: "wordcount", Diag: diag,
			Details: fmt.Sprintf("word count %d < minimum %d", words, p.MinWords)})
	case words > p.MaxWords:
		r.fail(FailWordcountHigh, Check{Name: "wordcount", Diag: diag,
			Details: fmt.Sprintf("word count %d > maximum %d", words, p.MaxWords)})
	default:
		r.pass(Check{Name: "wordcount", Diag: diag,
			Details: fmt.Sprintf("%d words (range %d to %d)", words, p.MinWords, p.MaxWords)})
	}

	// Banned phrases. Sleepy checked these twice, once against the policy list
	// and once against a high tension list that is a subset of it. One list
	// here, the one the prompt and script.Measure use.
	for i, h := range script.BannedCounts(body) {
		if i >= maxBannedReports {
			break
		}
		r.fail(FailBannedPhrase, Check{Name: "banned_phrase",
			Details: fmt.Sprintf("script contains banned phrase: %q (x%d)", h.Phrase, h.Count),
			Diag:    map[string]any{"phrase": h.Phrase, "count": h.Count}})
	}

	// Sleep safety.
	if fails := sleepSafety(body); len(fails) > 0 {
		for _, f := range fails {
			r.fail(FailPacing, Check{Name: "sleep_safety", Details: f})
		}
	} else {
		r.pass(Check{Name: "sleep_safety", Details: "all checks passed"})
	}

	// Title. The banned phrase check matches whole words. Sleepy matches
	// substrings here, so by reading its code a title like "Warm Meadow" fails on
	// "war". That is not carried over.
	switch t := strings.TrimSpace(title); {
	case t == "":
		r.fail(FailTitleInvalid, Check{Name: "title", Details: "title is empty"})
	case len([]rune(t)) < minTitleLen:
		r.fail(FailTitleInvalid, Check{Name: "title", Details: fmt.Sprintf("title too short (%d chars, min %d)", len([]rune(t)), minTitleLen)})
	case len([]rune(t)) > maxTitleLen:
		r.fail(FailTitleInvalid, Check{Name: "title", Details: fmt.Sprintf("title too long (%d chars, max %d)", len([]rune(t)), maxTitleLen)})
	case t == strings.ToUpper(t):
		r.fail(FailTitleInvalid, Check{Name: "title", Details: "title is ALL CAPS"})
	case len(script.BannedCounts(t)) > 0:
		r.fail(FailTitleInvalid, Check{Name: "title", Details: fmt.Sprintf("title contains banned phrase: %q", script.BannedCounts(t)[0].Phrase)})
	default:
		r.pass(Check{Name: "title", Details: fmt.Sprintf("title OK (%d chars): %q", len([]rune(t)), t)})
	}
	return r
}

// sleepSafety returns one message per failed safety check, none if all pass.
// These are Sleepy's llm.RunQA checks without the high tension list.
func sleepSafety(text string) []string {
	var fails []string

	if n := strings.Count(text, "!"); n > maxExclamations {
		fails = append(fails, fmt.Sprintf("too many exclamation marks: %d (max %d)", n, maxExclamations))
	}

	caps := 0
	for _, w := range strings.Fields(text) {
		clean := strings.Trim(w, ".,;:!?\"'()-[]{}/*#")
		if len(clean) < 3 || clean == "SSML" {
			continue
		}
		hasLetter, allUpper := false, true
		for _, r := range clean {
			if unicode.IsLetter(r) {
				hasLetter = true
				if !unicode.IsUpper(r) {
					allUpper = false
					break
				}
			}
		}
		if hasLetter && allUpper {
			caps++
		}
	}
	if caps > maxAllCapsWords {
		fails = append(fails, fmt.Sprintf("too many ALL CAPS words: %d (max %d)", caps, maxAllCapsWords))
	}

	if sents := splitSentences(text); len(sents) > 0 {
		total := 0
		for _, s := range sents {
			total += len(strings.Fields(s))
		}
		if avg := float64(total) / float64(len(sents)); avg > maxAvgSentenceLen {
			fails = append(fails, fmt.Sprintf("average sentence too long: %.1f words (max %.0f)", avg, maxAvgSentenceLen))
		}
	}

	// Repetition: a script that loops one sentence has few unique words.
	if all := strings.Fields(strings.ToLower(text)); len(all) > 50 {
		unique := make(map[string]struct{}, len(all))
		for _, w := range all {
			unique[strings.Trim(w, ".,;:!?\"'()-[]{}/*#")] = struct{}{}
		}
		if ratio := float64(len(unique)) / float64(len(all)); ratio < minUniqueWordRatio {
			fails = append(fails, fmt.Sprintf("high repetition: %.2f unique/total ratio (min %.2f)", ratio, minUniqueWordRatio))
		}
	}
	return fails
}

// splitSentences splits at newlines and at . ! ? (Sleepy's splitter). Lines
// that are headings or rules are skipped; a line with no punctuation counts as
// one sentence.
func splitSentences(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "---") {
			continue
		}
		var cur strings.Builder
		found := false
		for _, r := range line {
			cur.WriteRune(r)
			if r == '.' || r == '!' || r == '?' {
				if s := strings.TrimSpace(cur.String()); len(strings.Fields(s)) > 0 {
					out = append(out, s)
					found = true
				}
				cur.Reset()
			}
		}
		if !found {
			if s := strings.TrimSpace(cur.String()); len(strings.Fields(s)) > 0 {
				out = append(out, s)
			}
		}
	}
	return out
}
