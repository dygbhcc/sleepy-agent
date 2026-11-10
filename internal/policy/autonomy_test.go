package policy

import (
	"math"
	"strings"
	"testing"
)

func TestWilsonUpperMatchesTheNumbersInTheSpec(t *testing.T) {
	// The Day 9 measurements: 0 of 12 is 24 percent, 9 of 15 is 80 percent.
	for _, c := range []struct {
		k, n int
		want float64
	}{{0, 12, 0.24}, {9, 15, 0.80}, {13, 15, 0.96}} {
		if got := WilsonUpper(c.k, c.n); math.Abs(got-c.want) > 0.01 {
			t.Errorf("WilsonUpper(%d, %d) = %.3f, want about %.2f", c.k, c.n, got, c.want)
		}
	}
	if got := WilsonUpper(0, 0); got != 1 {
		t.Errorf("no tries must mean nothing is known, got %.2f", got)
	}
}

func enough() Evidence {
	return Evidence{OwnerLabelled: true, BadScripts: 100, BadLetThrough: 0}
}

func TestReviewNeedsNoEvidence(t *testing.T) {
	l, why := Autonomy{Level: LevelReview}.Granted()
	if l != LevelReview || why != "" {
		t.Fatalf("got %s %q", l, why)
	}
}

func TestUnlistedIsGrantedOnlyWithEnoughOwnerEvidence(t *testing.T) {
	l, why := Autonomy{Level: LevelUnlisted, Evidence: enough()}.Granted()
	if l != LevelUnlisted || why != "" {
		t.Fatalf("100 owner labelled bad scripts, none let through: got %s %q", l, why)
	}
}

func TestUnlistedIsRefusedWhenAnyEvidenceIsMissing(t *testing.T) {
	cases := map[string]struct {
		mutate func(*Evidence)
		want   string
	}{
		"labels by a model":    {func(e *Evidence) { e.OwnerLabelled = false }, "not the owner's"},
		"too few bad scripts":  {func(e *Evidence) { e.BadScripts, e.BadLetThrough = 29, 0 }, "29 bad scripts measured"},
		"too many let through": {func(e *Evidence) { e.BadScripts, e.BadLetThrough = 100, 9 }, "upper bound"},
		"nothing measured":     {func(e *Evidence) { *e = Evidence{} }, "0 bad scripts measured"},
	}
	for name, c := range cases {
		e := enough()
		c.mutate(&e)
		l, why := Autonomy{Level: LevelUnlisted, Evidence: e}.Granted()
		if l != LevelReview || !strings.Contains(why, c.want) {
			t.Errorf("%s: got %s %q, want review and a reason with %q", name, l, why, c.want)
		}
	}
}

func TestTheDay9MeasurementDoesNotEarnUnlisted(t *testing.T) {
	// strict-d: labels by Claude, 15 bad scripts, 9 passed the checks.
	a := Autonomy{Level: LevelUnlisted, Evidence: Evidence{BadScripts: 15, BadLetThrough: 9}}
	l, why := a.Granted()
	if l != LevelReview {
		t.Fatalf("got %s, the Day 9 evidence must not grant unlisted", l)
	}
	for _, part := range []string{"not the owner's", "15 bad scripts measured", "9 of 15", "upper bound 80 percent"} {
		if !strings.Contains(why, part) {
			t.Errorf("reason %q lacks %q", why, part)
		}
	}
}

func TestHaltAndUnknownLevelsFailClosed(t *testing.T) {
	if l, _ := (Autonomy{Level: LevelHalt, Evidence: enough()}).Granted(); l != LevelHalt {
		t.Errorf("halt with evidence must stay halt, got %s", l)
	}
	if l, _ := (Autonomy{Level: Level(7)}).Granted(); l != LevelHalt {
		t.Errorf("an unknown level must halt, got %s", l)
	}
}

func TestParseAutonomy(t *testing.T) {
	ok := `{"level": 1, "evidence": {"owner_labelled": false, "bad_scripts": 15, "bad_let_through": 9, "source": "strict-d", "measured_on": "2026-11-08"}}`
	a, err := ParseAutonomy([]byte(ok))
	if err != nil || a.Level != LevelReview || a.Evidence.BadLetThrough != 9 {
		t.Fatalf("got %+v, %v", a, err)
	}
	for name, bad := range map[string]string{
		"unknown field":    `{"level": 1, "levl": 2}`,
		"unknown level":    `{"level": 3}`,
		"negative level":   `{"level": -1}`,
		"counts disagree":  `{"level": 1, "evidence": {"bad_scripts": 3, "bad_let_through": 4}}`,
		"not json":         `level: 1`,
		"trailing garbage": `{"level": 1} {"level": 2}`,
	} {
		if _, err := ParseAutonomy([]byte(bad)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestTheBarWithNothingLetThroughIsThirtyFiveBadScripts(t *testing.T) {
	// Documented in docs/autonomy-policy.md: 0 of 34 is above 10 percent, 0 of 35 is not.
	for n, want := range map[int]Level{34: LevelReview, 35: LevelUnlisted} {
		a := Autonomy{Level: LevelUnlisted, Evidence: Evidence{OwnerLabelled: true, BadScripts: n}}
		if got, why := a.Granted(); got != want {
			t.Errorf("0 of %d: got %s (%s), want %s", n, got, why, want)
		}
	}
}
