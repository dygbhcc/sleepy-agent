package main

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"

	"sleepy-agent/internal/judge"
)

func vd(pass bool) struct {
	v   judge.Verdict
	err error
} {
	return struct {
		v   judge.Verdict
		err error
	}{v: judge.Verdict{Pass: pass, Calm: 4, Coherence: 4, Variety: 4, Narration: 4, MinScore: 3, Reason: "r"}}
}

func vErr() struct {
	v   judge.Verdict
	err error
} {
	return struct {
		v   judge.Verdict
		err error
	}{err: errors.New("down")}
}

func TestWilsonUpperIsWhatSmallCountsAllow(t *testing.T) {
	// Zero misses is no proof of zero: 30 bad scripts still allow about 11%,
	// 60 about 6%.
	if got := wilsonUpper(0, 30); math.Abs(got-0.1135) > 0.002 {
		t.Fatalf("0 of 30: %.4f", got)
	}
	if got := wilsonUpper(0, 60); math.Abs(got-0.0602) > 0.002 {
		t.Fatalf("0 of 60: %.4f", got)
	}
	if got := wilsonUpper(3, 10); got <= 0.3 || got >= 0.8 {
		t.Fatalf("3 of 10 must sit above the observed 30%%: %.3f", got)
	}
	if wilsonUpper(0, 0) != 1 {
		t.Fatal("no data allows anything")
	}
	if wilsonUpper(10, 10) > 1.0001 {
		t.Fatalf("a bound above 100%%: %.3f", wilsonUpper(10, 10))
	}
}

func twoSamples() []sample {
	return []sample{{name: "bad.md"}, {name: "good.md"}, {name: "err.md"}}
}

func TestTallySeparatesTheTwoKindsOfMistake(t *testing.T) {
	samples := []sample{{name: "a"}, {name: "b"}, {name: "c"}, {name: "d"}, {name: "e"}, {name: "f"}}
	labels := map[string]string{"a": "bad", "b": "bad", "c": "good", "d": "good", "e": "bad", "f": ""}
	vs := verdicts{"a": vd(false), "b": vd(true), "c": vd(false), "d": vd(true), "e": vErr(), "f": vd(true)}
	c := tally(samples, labels, vs)
	if c.rejectsBad != 1 || c.missesBad != 1 || c.rejectsGood != 1 || c.passesGood != 1 {
		t.Fatalf("%+v", c)
	}
	if c.bad() != 2 || c.good() != 2 {
		t.Fatalf("an errored or unlabelled script must not count: bad %d good %d", c.bad(), c.good())
	}
}

func TestCombineVerdictsNeedsEveryModelToPass(t *testing.T) {
	samples := twoSamples()
	a := named{"a", verdicts{"bad.md": vd(true), "good.md": vd(true), "err.md": vd(true)}}
	b := named{"b", verdicts{"bad.md": vd(false), "good.md": vd(true), "err.md": vErr()}}
	got := combineVerdicts(samples, []named{a, b})
	if got["bad.md"].err != nil || got["bad.md"].v.Pass {
		t.Fatalf("one model rejects: %+v", got["bad.md"])
	}
	if !got["good.md"].v.Pass {
		t.Fatalf("both pass: %+v", got["good.md"])
	}
	if got["err.md"].err == nil {
		t.Fatal("a model that errored leaves the panel unconfirmed")
	}
}

func TestPanelReportShowsAgreementAndTheLetThroughBound(t *testing.T) {
	samples := []sample{{name: "a"}, {name: "b"}, {name: "c"}, {name: "d"}}
	labels := map[string]string{"a": "bad", "b": "bad", "c": "good", "d": "good"}
	m1 := named{"m1", verdicts{"a": vd(true), "b": vd(false), "c": vd(true), "d": vd(true)}}
	m2 := named{"m2", verdicts{"a": vd(false), "b": vd(false), "c": vd(true), "d": vd(false)}}
	panel := combineVerdicts(samples, []named{m1, m2})
	var out bytes.Buffer
	panelReport(&out, samples, labels, []named{m1, m2}, panel)
	s := out.String()
	for _, want := range []string{
		"| m1 | m2 | 2 | 4 |", // they differ on a and d
		"| m1 | 1 | 1 | 0 | 2 |",
		"| m2 | 2 | 0 | 1 | 1 |",
		"| panel (all must pass) | 2 | 0 | 1 | 1 |",
		"(1 of 2)", // m1 let one of two bad scripts through
		"(0 of 2)",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("report lacks %q:\n%s", want, s)
		}
	}
}

func TestPanelReportWithOneModelHasNoPairTableOrPanelRow(t *testing.T) {
	samples := []sample{{name: "a"}}
	m1 := named{"m1", verdicts{"a": vd(true)}}
	var out bytes.Buffer
	panelReport(&out, samples, map[string]string{"a": "good"}, []named{m1}, m1.vs)
	s := out.String()
	if strings.Contains(s, "model A") || strings.Contains(s, "panel (all must pass)") {
		t.Fatalf("one model has nothing to compare:\n%s", s)
	}
	if !strings.Contains(s, "| m1 | 0 | 0 | 0 | 1 | - |") {
		t.Fatalf("no bad scripts, no bound:\n%s", s)
	}
}
