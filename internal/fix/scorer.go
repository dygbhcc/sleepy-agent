package fix

import (
	"math"
	"sort"
	"sync"
)

// Weights follow Sleepy's scorer: success rate first, speed second. Cost is
// left out until it is measured.
const (
	weightSuccessRate = 0.70
	weightAvgAttempts = 0.30
)

// Outcome is what happened after a plan was applied: the next attempt either
// passed the gate or it did not.
type Outcome struct {
	PlanID         string
	Success        bool
	AttemptsToPass int // attempts the step had used when the outcome was known
}

// Stat is the record of one plan.
type Stat struct {
	PlanID      string
	Total       int
	Successes   int
	AvgAttempts float64 // over the successes only, 0 when there are none
}

// Scorer remembers how each plan did and ranks candidates. It is safe for
// concurrent use. Its memory lives as long as the process: Sleepy keeps it in
// a database, this repo has no history to load yet.
//
// It differs from Sleepy's scorer in one place, by reading Sleepy's code, not
// from anything seen in production. There a plan that never worked can still
// outrank a plan nobody has tried, because only its attempt count is rewarded.
// Here the success rate is smoothed, (successes+1)/(total+2), so an untried
// plan starts at 0.5, a plan that failed twice at 0.25, and attempts count only
// for plans that succeeded.
type Scorer struct {
	mu    sync.RWMutex
	stats map[string]*counts
}

type counts struct {
	total, successes, sumAttempts int
}

// NewScorer returns a scorer that starts from past outcomes.
func NewScorer(past ...Outcome) *Scorer {
	s := &Scorer{stats: make(map[string]*counts)}
	for _, o := range past {
		s.record(o)
	}
	return s
}

// Record adds one outcome.
func (s *Scorer) Record(o Outcome) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.record(o)
}

func (s *Scorer) record(o Outcome) {
	c := s.stats[o.PlanID]
	if c == nil {
		c = &counts{}
		s.stats[o.PlanID] = c
	}
	c.total++
	if o.Success {
		c.successes++
		c.sumAttempts += o.AttemptsToPass
	}
}

func score(c *counts) float64 {
	if c == nil {
		c = &counts{}
	}
	rate := float64(c.successes+1) / float64(c.total+2)
	attempt := 0.5 // neutral until there is a success to measure
	if c.successes > 0 {
		avg := float64(c.sumAttempts) / float64(c.successes)
		attempt = 1 - math.Min(avg/5, 1)
	}
	return weightSuccessRate*rate + weightAvgAttempts*attempt
}

// Best returns the index of the highest scoring candidate. Ties go to the
// earlier one, so the catalog order is the default priority.
func (s *Scorer) Best(candidates []Plan) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	best, bestScore := 0, math.Inf(-1)
	for i, c := range candidates {
		if sc := score(s.stats[c.ID]); sc > bestScore+1e-12 {
			best, bestScore = i, sc
		}
	}
	return best
}

// Stats lists every plan that has an outcome, sorted by plan ID.
func (s *Scorer) Stats() []Stat {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Stat, 0, len(s.stats))
	for id, c := range s.stats {
		st := Stat{PlanID: id, Total: c.total, Successes: c.successes}
		if c.successes > 0 {
			st.AvgAttempts = float64(c.sumAttempts) / float64(c.successes)
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PlanID < out[j].PlanID })
	return out
}
