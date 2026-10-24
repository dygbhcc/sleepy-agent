package judge

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Reviewer is anything that scores a script the way Judge does.
type Reviewer interface {
	Review(ctx context.Context, topic, title, body string) (Verdict, error)
}

// Member is one reviewer on a panel, with the name it is reported under.
type Member struct {
	Name     string
	Reviewer Reviewer
}

// Result is what one member said about one script.
type Result struct {
	Name    string
	Verdict Verdict
	Err     error
}

// Panel asks several reviewers, ideally from different model families, and
// passes a script only when all of them pass it. A reviewer that wrote the
// script shares its blind spots, a reviewer from another family does not share
// all of them. The panel is as much a thing to measure as a judge: whether it
// finds what a single judge misses is for cmd/evalstats to say.
//
// A Panel is a Reviewer, so it can stand in for one wherever a reviewer goes.
type Panel struct {
	Members []Member
}

// ReviewEach asks every member in turn and returns every answer. It does not
// stop at the first rejection, so the members can be compared.
func (p Panel) ReviewEach(ctx context.Context, topic, title, body string) []Result {
	out := make([]Result, 0, len(p.Members))
	for _, m := range p.Members {
		v, err := m.Reviewer.Review(ctx, topic, title, body)
		out = append(out, Result{Name: m.Name, Verdict: v, Err: err})
	}
	return out
}

// Review implements Reviewer: the combined verdict of all members.
func (p Panel) Review(ctx context.Context, topic, title, body string) (Verdict, error) {
	if len(p.Members) == 0 {
		return Verdict{}, errors.New("judge: a panel needs at least one member")
	}
	return Combine(p.ReviewEach(ctx, topic, title, body))
}

// Combine merges member answers into one verdict. It passes only when every
// member answered and passed. Each score is the lowest any member gave, the
// reason names every member that rejected, and a member that could not answer
// makes the whole thing an error: a panel that cannot reach a member has not
// confirmed anything.
func Combine(results []Result) (Verdict, error) {
	if len(results) == 0 {
		return Verdict{}, errors.New("judge: no results to combine")
	}
	var errs []error
	for _, r := range results {
		if r.Err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", r.Name, r.Err))
		}
	}
	if len(errs) > 0 {
		return Verdict{}, errors.Join(errs...)
	}
	v := results[0].Verdict
	v.Pass = true
	var why, proof []string
	for _, r := range results {
		x := r.Verdict
		v.Calm, v.Coherence = min(v.Calm, x.Calm), min(v.Coherence, x.Coherence)
		v.Variety, v.Narration = min(v.Variety, x.Variety), min(v.Narration, x.Narration)
		v.MinScore = max(v.MinScore, x.MinScore)
		if !x.Pass {
			v.Pass = false
			why = append(why, r.Name+": "+x.Reason)
			if x.Evidence != "" && x.Evidence != "none" {
				proof = append(proof, r.Name+": "+x.Evidence)
			}
		}
	}
	if len(results) > 1 {
		v.PromptTokens, v.CompletionTokens = 0, 0
		for _, r := range results {
			v.PromptTokens += r.Verdict.PromptTokens
			v.CompletionTokens += r.Verdict.CompletionTokens
		}
	}
	switch {
	case v.Pass:
		v.Reason, v.Evidence = "none", "none"
	default:
		v.Reason, v.Evidence = strings.Join(why, "; "), strings.Join(proof, "; ")
	}
	return v, nil
}
