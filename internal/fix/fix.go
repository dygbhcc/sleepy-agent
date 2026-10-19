// Package fix is the fix engine: given a QA report that said no, it picks a
// directed repair for the next attempt instead of asking the model the same
// question again.
//
// Ported from Sleepy (internal/jobs/fix.go, fix_catalog.go, fix_scorer.go),
// reduced to the script stage, the only one with a gate in this repo. The
// catalog keeps Sleepy's plans and their order. What differs:
//
//   - The repair note names what was wrong (the banned words found, the word
//     count against the limit), taken from the report, where Sleepy's notes are
//     generic. The failure code selects the plan, the report's numbers fill it.
//   - TITLE_INVALID asks for a new title only and keeps the script. Sleepy
//     regenerates the whole script for a bad title.
//   - A plan is tried once per run. When the catalog has nothing left for the
//     failure, the caller falls back to a plain retry.
//   - The scorer smooths the success rate, see Scorer.
//
// The engine never touches a gate: the validator that said no is the one that
// judges the repaired attempt.
package fix

import (
	"fmt"
	"strings"

	"sleepy-agent/internal/qa"
	"sleepy-agent/internal/script"
)

// Target says how the engine computes a word target for a plan.
type Target string

const (
	TargetKeep      Target = ""           // leave the policy target alone
	TargetPolicy    Target = "policy"     // the target of the policy that was judged
	TargetMinPadded Target = "min_padded" // minimum words plus Buffer, capped at the maximum
	TargetMaxCapped Target = "max_capped" // maximum words minus Buffer, floored at the minimum
)

// Plan is one repair. The fields above Detail come from the catalog, the last
// two are filled by Engine.Decide from the report that failed.
type Plan struct {
	ID          string      `json:"id"`
	FailType    qa.FailType `json:"fail_type"`
	Mode        script.Mode `json:"mode,omitempty"`
	Temperature float64     `json:"temperature,omitempty"`
	Instruction string      `json:"instruction,omitempty"`
	Target      Target      `json:"target,omitempty"`
	Buffer      int         `json:"buffer,omitempty"`

	TargetWords int    `json:"target_words,omitempty"`
	Detail      string `json:"detail,omitempty"`
}

// Adjust turns a plan into what the script generator needs. prevTitle and
// prevBody are what the rejected attempt produced.
func (p Plan) Adjust(prevTitle, prevBody string) script.Adjust {
	return script.Adjust{
		Mode:             p.Mode,
		Temperature:      p.Temperature,
		ExtraInstruction: strings.TrimSpace(p.Instruction + " " + p.Detail),
		TargetWords:      p.TargetWords,
		PreviousTitle:    prevTitle,
		PreviousBody:     prevBody,
	}
}

// catalog lists the plans per failure, in priority order. With no history the
// first untried plan is chosen, so the order is the order of escalation.
func catalog() map[qa.FailType][]Plan {
	c := map[qa.FailType][]Plan{}
	add := func(ps ...Plan) {
		for _, p := range ps {
			c[p.FailType] = append(c[p.FailType], p)
		}
	}
	add(
		// Too short: keep what was written and add to it, then ask for richer
		// additions, then write the whole script again with a padded target.
		Plan{ID: "wc_low_continue", FailType: qa.FailWordcountLow, Mode: script.ModeContinue, Temperature: 0.7, Target: TargetPolicy},
		Plan{ID: "wc_low_continue_expand", FailType: qa.FailWordcountLow, Mode: script.ModeContinue, Temperature: 0.8, Target: TargetPolicy,
			Instruction: "Add rich sensory details, new scenes, and diverse imagery in the continuation."},
		Plan{ID: "wc_low_regen", FailType: qa.FailWordcountLow, Temperature: 0.7, Target: TargetMinPadded, Buffer: 300,
			Instruction: "Write a complete script from scratch. Expand descriptions, add sensory detail, and use longer paragraphs."},

		// Too long: aim below the maximum, with a bigger margin the second time.
		Plan{ID: "wc_high_max_capped", FailType: qa.FailWordcountHigh, Temperature: 0.5, Target: TargetMaxCapped, Buffer: 150,
			Instruction: "The previous script was too long. Be more concise, stop when you reach the target word count, and never go over the maximum."},
		Plan{ID: "wc_high_aggressive", FailType: qa.FailWordcountHigh, Temperature: 0.3, Target: TargetMaxCapped, Buffer: 250,
			Instruction: "The script must be shorter. Use fewer paragraphs, make every sentence count, and stop as soon as you are within range."},

		// Banned words: colder, and told which words to avoid.
		Plan{ID: "banned_conservative", FailType: qa.FailBannedPhrase, Temperature: 0.3,
			Instruction: "Avoid every banned word. Use only the gentlest, most calming vocabulary."},
		Plan{ID: "banned_ultra_safe", FailType: qa.FailBannedPhrase, Temperature: 0.2,
			Instruction: "Do not use any word associated with tension, conflict, danger, or excitement. Every word must be soft and peaceful."},

		// Pacing and repetition: more variety, by replacing, not by adding.
		Plan{ID: "pacing_variety", FailType: qa.FailPacing, Temperature: 0.8, Target: TargetMaxCapped, Buffer: 200,
			Instruction: "Vary your imagery and phrasing throughout. Never repeat a sentence or a close paraphrase. Each paragraph must introduce fresh sensory details. Get variety by replacing repetitive content, not by adding more, and stay within the word limit."},
		Plan{ID: "pacing_aggressive", FailType: qa.FailPacing, Temperature: 0.9, Target: TargetMaxCapped, Buffer: 200,
			Instruction: "The previous script was repetitive. Every paragraph must use completely different imagery, and no two paragraphs may describe a similar scene. Vary sentence length and structure. Write fewer, more diverse paragraphs, and stay strictly within the word limit."},

		// Bad title: the script is fine, ask for another title.
		Plan{ID: "title_retitle", FailType: qa.FailTitleInvalid, Mode: script.ModeRetitle, Temperature: 0.6},
	)
	return c
}

// Engine picks repairs. It is safe for concurrent use.
type Engine struct {
	catalog map[qa.FailType][]Plan
	scorer  *Scorer
}

// NewEngine returns an engine with the default catalog. Past outcomes seed the
// scorer, and may be none.
func NewEngine(past ...Outcome) *Engine {
	return &Engine{catalog: catalog(), scorer: NewScorer(past...)}
}

// Decide picks the plan for a failed report, or reports false when there is
// none: the report passed, or every plan for this failure was already tried on
// this run. The caller then falls back to a plain retry. tried lists the IDs
// already used on the run.
func (e *Engine) Decide(r qa.Report, tried []string) (Plan, bool) {
	if r.Pass || r.FailType == "" {
		return Plan{}, false
	}
	used := make(map[string]bool, len(tried))
	for _, id := range tried {
		used[id] = true
	}
	var candidates []Plan
	for _, p := range e.catalog[r.FailType] {
		if !used[p.ID] {
			candidates = append(candidates, p)
		}
	}
	if len(candidates) == 0 {
		return Plan{}, false
	}
	p := candidates[e.scorer.Best(candidates)]
	p.TargetWords = targetWords(p, r)
	p.Detail = detail(r)
	return p, true
}

// Record tells the scorer how a plan did.
func (e *Engine) Record(o Outcome) { e.scorer.Record(o) }

// Stats lists the record of every plan that has an outcome.
func (e *Engine) Stats() []Stat { return e.scorer.Stats() }

// targetWords computes the word target a plan asks for, 0 to leave it alone.
func targetWords(p Plan, r qa.Report) int {
	d := diag(r, "wordcount")
	min, max, target := intOf(d["min_words"]), intOf(d["max_words"]), intOf(d["target_words"])
	switch p.Target {
	case TargetPolicy:
		return target
	case TargetMinPadded:
		if min == 0 {
			return 0
		}
		if t := min + p.Buffer; max == 0 || t <= max {
			return t
		}
		return max
	case TargetMaxCapped:
		if max == 0 {
			return 0
		}
		if t := max - p.Buffer; t >= min {
			return t
		}
		return min
	}
	return 0
}

// detail says what was wrong, in the report's own numbers and words.
func detail(r qa.Report) string {
	switch r.FailType {
	case qa.FailWordcountLow:
		d := diag(r, "wordcount")
		return fmt.Sprintf("The previous script had %d words. The minimum is %d and the target is %d.",
			intOf(d["actual_words"]), intOf(d["min_words"]), intOf(d["target_words"]))
	case qa.FailWordcountHigh:
		d := diag(r, "wordcount")
		return fmt.Sprintf("The previous script had %d words. The maximum is %d.",
			intOf(d["actual_words"]), intOf(d["max_words"]))
	case qa.FailBannedPhrase:
		var words []string
		for _, c := range r.Checks {
			if c.Name == "banned_phrase" && !c.Pass {
				if w, ok := c.Diag["phrase"].(string); ok {
					words = append(words, fmt.Sprintf("%q", w))
				}
			}
		}
		if len(words) == 0 {
			return ""
		}
		return "The previous script used these banned words, do not use them: " + strings.Join(words, ", ") + "."
	case qa.FailPacing:
		return "The previous script failed: " + failedDetails(r, "sleep_safety") + "."
	case qa.FailTitleInvalid:
		return "The previous title was rejected: " + failedDetails(r, "title") + "."
	}
	return ""
}

func failedDetails(r qa.Report, check string) string {
	var out []string
	for _, c := range r.Checks {
		if c.Name == check && !c.Pass {
			out = append(out, strings.TrimRight(c.Details, "."))
		}
	}
	return strings.Join(out, "; ")
}

func diag(r qa.Report, check string) map[string]any {
	for _, c := range r.Checks {
		if c.Name == check && c.Diag != nil {
			return c.Diag
		}
	}
	return nil
}

// intOf reads a diagnostic number, whether the report was just built (int) or
// read back from JSON (float64).
func intOf(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	}
	return 0
}

// Fixed reports whether the check behind a failure passes in a report: the
// question "did the plan chosen for this failure do its job", which is not the
// same as "did the run pass". A continuation can cure a short script and still
// leave a banned word that only shows once the length is right.
func Fixed(r qa.Report, ft qa.FailType) bool {
	var check string
	switch ft {
	case qa.FailWordcountLow, qa.FailWordcountHigh:
		check = "wordcount"
	case qa.FailBannedPhrase:
		check = "banned_phrase"
	case qa.FailPacing:
		check = "sleep_safety"
	case qa.FailTitleInvalid:
		check = "title"
	default:
		return r.Pass
	}
	for _, c := range r.Checks {
		if c.Name == check && !c.Pass {
			return false
		}
	}
	return true
}
