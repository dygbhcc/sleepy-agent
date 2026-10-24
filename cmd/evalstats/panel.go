package main

import (
	"fmt"
	"io"
	"math"

	"sleepy-agent/internal/judge"
)

// named is one judge model's verdicts over all scripts.
type named struct {
	name string
	vs   verdicts
}

// combineVerdicts is the panel's answer per script: it passes only when every
// model answered and passed.
func combineVerdicts(samples []sample, byModel []named) verdicts {
	out := verdicts{}
	for _, s := range samples {
		var rs []judge.Result
		for _, b := range byModel {
			r := b.vs[s.name]
			rs = append(rs, judge.Result{Name: b.name, Verdict: r.v, Err: r.err})
		}
		v, err := judge.Combine(rs)
		out[s.name] = struct {
			v   judge.Verdict
			err error
		}{v, err}
	}
	return out
}

// wilsonUpper is the upper end of the 95% Wilson interval for k events in n
// trials. With nothing observed it is about 3.8/n for small n, close to the
// "rule of three": zero misses in 30 bad scripts still allows a true miss
// rate near 10%.
func wilsonUpper(k, n int) float64 {
	if n == 0 {
		return 1
	}
	const z = 1.96
	p := float64(k) / float64(n)
	nn := float64(n)
	centre := p + z*z/(2*nn)
	margin := z * math.Sqrt(p*(1-p)/nn+z*z/(4*nn*nn))
	return (centre + margin) / (1 + z*z/nn)
}

// confusion counts one checker against the human labels. Scripts the checker
// could not judge are left out.
type confusion struct {
	rejectsBad, missesBad, rejectsGood, passesGood int
}

func (c confusion) bad() int  { return c.rejectsBad + c.missesBad }
func (c confusion) good() int { return c.rejectsGood + c.passesGood }

func tally(samples []sample, labels map[string]string, vs verdicts) confusion {
	var c confusion
	for _, s := range samples {
		label := labels[s.name]
		r, ok := vs[s.name]
		if (label != "good" && label != "bad") || !ok || r.err != nil {
			continue
		}
		switch {
		case label == "bad" && !r.v.Pass:
			c.rejectsBad++
		case label == "bad":
			c.missesBad++
		case !r.v.Pass:
			c.rejectsGood++
		default:
			c.passesGood++
		}
	}
	return c
}

// panelReport compares the judge models with each other and with the labels.
// The number that matters for autonomy is how many bad scripts a checker lets
// through, with the interval that says how little a small count proves.
func panelReport(w io.Writer, samples []sample, labels map[string]string, byModel []named, panel verdicts) {
	if len(byModel) > 1 {
		fmt.Fprintln(w, "\nDo the models agree with each other (same pass or reject)?")
		fmt.Fprintln(w, "\n| model A | model B | same decision | of |")
		fmt.Fprintln(w, "|---|---|---|---|")
		for i := 0; i < len(byModel); i++ {
			for j := i + 1; j < len(byModel); j++ {
				same, of := 0, 0
				for _, s := range samples {
					a, b := byModel[i].vs[s.name], byModel[j].vs[s.name]
					if a.err != nil || b.err != nil {
						continue
					}
					of++
					if a.v.Pass == b.v.Pass {
						same++
					}
				}
				fmt.Fprintf(w, "| %s | %s | %d | %d |\n", byModel[i].name, byModel[j].name, same, of)
			}
		}
	}
	fmt.Fprintln(w, "\nAgainst the human labels:")
	fmt.Fprintln(w, "\n| checker | bad scripts rejected | bad scripts let through | good scripts rejected | good scripts passed | let-through rate, 95% upper bound |")
	fmt.Fprintln(w, "|---|---|---|---|---|---|")
	row := func(name string, vs verdicts) {
		c := tally(samples, labels, vs)
		bound := "-"
		if c.bad() > 0 {
			bound = fmt.Sprintf("%.0f%% (%d of %d)", wilsonUpper(c.missesBad, c.bad())*100, c.missesBad, c.bad())
		}
		fmt.Fprintf(w, "| %s | %d | %d | %d | %d | %s |\n", name, c.rejectsBad, c.missesBad, c.rejectsGood, c.passesGood, bound)
	}
	for _, b := range byModel {
		row(b.name, b.vs)
	}
	if len(byModel) > 1 {
		row("panel (all must pass)", panel)
	}
	fmt.Fprintln(w, "\nA let-through bad script is the costly mistake: it would have been published. The upper bound is what the count allows the true rate to be; with few bad scripts it stays high even at zero misses.")
}
