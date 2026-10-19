package fix

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"sleepy-agent/internal/policy"
	"sleepy-agent/internal/qa"
	"sleepy-agent/internal/script"
)

var p5 = policy.ForDuration(5) // 500 to 800 words, target 650

// Reports are made by the real gate, so the diagnostics are the real ones.
func shortReport() qa.Report { return qa.Script("Velvet Meadow", script.SampleScript(4), p5) }
func longReport() qa.Report  { return qa.Script("Velvet Meadow", script.SampleScript(20), p5) }
func bannedReport() qa.Report {
	body := strings.Replace(script.SampleScript(12), "river", "disappear", 2)
	body = strings.Replace(body, "meadow", "fear", 1)
	return qa.Script("Velvet Meadow", body, p5)
}
func pacingReport() qa.Report {
	body := strings.Replace(script.SampleScript(12), "tonight.", "tonight!", 3)
	return qa.Script("Velvet Meadow", body, p5)
}
func titleReport() qa.Report { return qa.Script("VELVET MEADOW", script.SampleScript(12), p5) }

func TestTheReportsAboveFailForTheReasonTheirNamesSay(t *testing.T) {
	for want, r := range map[qa.FailType]qa.Report{
		qa.FailWordcountLow: shortReport(), qa.FailWordcountHigh: longReport(), qa.FailBannedPhrase: bannedReport(),
		qa.FailPacing: pacingReport(), qa.FailTitleInvalid: titleReport(),
	} {
		if r.Pass || r.FailType != want {
			t.Fatalf("want %s, got pass=%v %s (%s)", want, r.Pass, r.FailType, r.Summary())
		}
	}
}

func TestEveryFailureTheGateCanReturnHasAtLeastOnePlan(t *testing.T) {
	e := NewEngine()
	for _, ft := range []qa.FailType{qa.FailWordcountLow, qa.FailWordcountHigh, qa.FailBannedPhrase, qa.FailPacing, qa.FailTitleInvalid} {
		if len(e.catalog[ft]) == 0 {
			t.Errorf("no plan for %s", ft)
		}
	}
	seen := map[string]bool{}
	for ft, plans := range e.catalog {
		for _, p := range plans {
			if p.FailType != ft || p.ID == "" || seen[p.ID] {
				t.Errorf("plan %+v is filed under %s, has no ID or repeats one", p, ft)
			}
			seen[p.ID] = true
		}
	}
}

func TestWithoutHistoryThePlansComeInCatalogOrder(t *testing.T) {
	e := NewEngine()
	var tried []string
	for _, want := range []string{"wc_low_continue", "wc_low_continue_expand", "wc_low_regen"} {
		p, ok := e.Decide(shortReport(), tried)
		if !ok || p.ID != want {
			t.Fatalf("want %s, got %q ok=%v", want, p.ID, ok)
		}
		tried = append(tried, p.ID)
	}
	if p, ok := e.Decide(shortReport(), tried); ok {
		t.Fatalf("every plan was tried, got %s", p.ID)
	}
}

func TestNothingToDecideForAPassingReportOrOneWithoutAFailType(t *testing.T) {
	e := NewEngine()
	good := qa.Script("Velvet Meadow", script.SampleScript(12), p5)
	if !good.Pass {
		t.Fatal(good.Summary())
	}
	if _, ok := e.Decide(good, nil); ok {
		t.Error("a passing report needs no fix")
	}
	if _, ok := e.Decide(qa.Report{Pass: false}, nil); ok {
		t.Error("a failure without a type has no catalog entry")
	}
}

func TestAPlanThatWorkedBeatsAnEarlierOneInTheCatalog(t *testing.T) {
	e := NewEngine(
		Outcome{PlanID: "banned_ultra_safe", Success: true, AttemptsToPass: 2},
		Outcome{PlanID: "banned_ultra_safe", Success: true, AttemptsToPass: 2},
	)
	p, _ := e.Decide(bannedReport(), nil)
	if p.ID != "banned_ultra_safe" {
		t.Fatalf("got %s", p.ID)
	}
}

func TestAPlanThatKeepsFailingLosesToOneNobodyTried(t *testing.T) {
	// The reason for the smoothed success rate, see Scorer.
	e := NewEngine(
		Outcome{PlanID: "wc_low_continue", Success: false, AttemptsToPass: 2},
		Outcome{PlanID: "wc_low_continue", Success: false, AttemptsToPass: 2},
	)
	p, _ := e.Decide(shortReport(), nil)
	if p.ID == "wc_low_continue" {
		t.Fatal("a plan that failed twice must rank below an untried one")
	}
	if p.ID != "wc_low_continue_expand" {
		t.Fatalf("the next in catalog order, got %s", p.ID)
	}
}

func TestOneFailureDoesNotBuryAPlanForever(t *testing.T) {
	// 0 of 1 scores 0.33 against 0.5 for an untried plan, so a single bad run
	// moves it back but a proven plan can still beat the untried ones.
	e := NewEngine(
		Outcome{PlanID: "wc_low_continue", Success: false, AttemptsToPass: 2},
		Outcome{PlanID: "wc_low_regen", Success: true, AttemptsToPass: 2},
		Outcome{PlanID: "wc_low_regen", Success: true, AttemptsToPass: 2},
		Outcome{PlanID: "wc_low_regen", Success: true, AttemptsToPass: 2},
	)
	if p, _ := e.Decide(shortReport(), nil); p.ID != "wc_low_regen" {
		t.Fatalf("a plan with three successes should lead, got %s", p.ID)
	}
}

func TestFasterSuccessBreaksATieInSuccessRate(t *testing.T) {
	e := NewEngine(
		Outcome{PlanID: "pacing_variety", Success: true, AttemptsToPass: 3},
		Outcome{PlanID: "pacing_aggressive", Success: true, AttemptsToPass: 2},
	)
	if p, _ := e.Decide(pacingReport(), nil); p.ID != "pacing_aggressive" {
		t.Fatalf("got %s", p.ID)
	}
}

func TestTheWordTargetIsComputedFromTheLimitsInTheReport(t *testing.T) {
	cases := []struct {
		plan Plan
		rep  qa.Report
		want int
	}{
		{Plan{Target: TargetPolicy}, shortReport(), 650},
		{Plan{Target: TargetMinPadded, Buffer: 100}, shortReport(), 600},
		{Plan{Target: TargetMinPadded, Buffer: 300}, shortReport(), 800},
		{Plan{Target: TargetMinPadded, Buffer: 900}, shortReport(), 800}, // never above the maximum
		{Plan{Target: TargetMaxCapped, Buffer: 150}, longReport(), 650},
		{Plan{Target: TargetMaxCapped, Buffer: 250}, longReport(), 550},
		{Plan{Target: TargetMaxCapped, Buffer: 900}, longReport(), 500}, // never below the minimum
		{Plan{Target: TargetKeep}, longReport(), 0},
	}
	for i, c := range cases {
		if got := targetWords(c.plan, c.rep); got != c.want {
			t.Errorf("case %d (%+v): got %d, want %d", i, c.plan, got, c.want)
		}
	}
	if got := targetWords(Plan{Target: TargetPolicy}, titleReport()); got != 650 {
		t.Errorf("the wordcount check is in every report, even one that failed on the title: got %d", got)
	}
	if got := targetWords(Plan{Target: TargetPolicy}, qa.Report{}); got != 0 {
		t.Errorf("a report without diagnostics leaves the target alone, got %d", got)
	}
}

func TestDecideFillsTheTargetAndTheDetailFromTheReport(t *testing.T) {
	e := NewEngine()
	p, _ := e.Decide(shortReport(), nil)
	if p.TargetWords != 650 || p.Mode != script.ModeContinue {
		t.Fatalf("%+v", p)
	}
	for _, want := range []string{"208 words", "minimum is 500", "target is 650"} {
		if !strings.Contains(p.Detail, want) {
			t.Errorf("detail %q should contain %q", p.Detail, want)
		}
	}
}

func TestTheRepairNoteNamesWhatWasWrong(t *testing.T) {
	e := NewEngine()
	cases := []struct {
		rep  qa.Report
		want []string
	}{
		{longReport(), []string{"had 1040 words", "maximum is 800"}},
		{bannedReport(), []string{`"disappear"`, `"fear"`, "do not use them"}},
		{pacingReport(), []string{"too many exclamation marks: 3"}},
		{titleReport(), []string{"title is ALL CAPS"}},
	}
	for _, c := range cases {
		p, ok := e.Decide(c.rep, nil)
		if !ok {
			t.Fatalf("no plan for %s", c.rep.FailType)
		}
		note := p.Adjust("T", "body").ExtraInstruction
		for _, w := range c.want {
			if !strings.Contains(note, w) {
				t.Errorf("%s: note %q should contain %q", c.rep.FailType, note, w)
			}
		}
	}
}

func TestAReportReadBackFromJSONGivesTheSameDecision(t *testing.T) {
	// The worker keeps the report as JSON, and numbers come back as float64.
	b, err := json.Marshal(shortReport())
	if err != nil {
		t.Fatal(err)
	}
	var back qa.Report
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	e := NewEngine()
	a, _ := e.Decide(shortReport(), nil)
	c, _ := e.Decide(back, nil)
	if a != c {
		t.Fatalf("live %+v\nfrom JSON %+v", a, c)
	}
}

func TestAdjustCarriesTheWholePlanToTheGenerator(t *testing.T) {
	p := Plan{Mode: script.ModeContinue, Temperature: 0.8, Instruction: "Add detail.", Detail: "It had 5 words.", TargetWords: 700}
	a := p.Adjust("Old Title", "old body")
	want := script.Adjust{Mode: script.ModeContinue, Temperature: 0.8, ExtraInstruction: "Add detail. It had 5 words.",
		TargetWords: 700, PreviousTitle: "Old Title", PreviousBody: "old body"}
	if a != want {
		t.Fatalf("got %+v", a)
	}
	if got := (Plan{Detail: "Only detail."}).Adjust("", "").ExtraInstruction; got != "Only detail." {
		t.Fatalf("no stray spaces: %q", got)
	}
	if got := (Plan{}).Adjust("", "").ExtraInstruction; got != "" {
		t.Fatalf("no note when there is nothing to say: %q", got)
	}
}

func TestStatsCountOutcomesAndAverageTheAttemptsOfSuccessesOnly(t *testing.T) {
	e := NewEngine()
	e.Record(Outcome{PlanID: "a", Success: true, AttemptsToPass: 2})
	e.Record(Outcome{PlanID: "a", Success: true, AttemptsToPass: 4})
	e.Record(Outcome{PlanID: "a", Success: false, AttemptsToPass: 9})
	e.Record(Outcome{PlanID: "b", Success: false, AttemptsToPass: 3})
	got := e.Stats()
	if len(got) != 2 || got[0] != (Stat{PlanID: "a", Total: 3, Successes: 2, AvgAttempts: 3}) ||
		got[1] != (Stat{PlanID: "b", Total: 1}) {
		t.Fatalf("%+v", got)
	}
}

func TestEngineIsSafeForConcurrentUse(t *testing.T) {
	e := NewEngine()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 30; j++ {
				e.Decide(shortReport(), nil)
				e.Record(Outcome{PlanID: "wc_low_continue", Success: j%2 == 0, AttemptsToPass: 2})
				e.Stats()
			}
		}()
	}
	wg.Wait()
}

func TestDecideDoesNotChangeTheCatalog(t *testing.T) {
	e := NewEngine()
	e.Decide(shortReport(), nil)
	if p := e.catalog[qa.FailWordcountLow][0]; p.TargetWords != 0 || p.Detail != "" {
		t.Fatalf("the catalog entry must stay a template: %+v", p)
	}
}

func TestFixedLooksAtTheCheckBehindTheFailureNotAtTheWholeRun(t *testing.T) {
	// A report that failed on banned words only: the length check passed.
	banned := bannedReport()
	if !Fixed(banned, qa.FailWordcountLow) || !Fixed(banned, qa.FailWordcountHigh) {
		t.Error("the word count was fine, so a length plan counts as having worked")
	}
	if Fixed(banned, qa.FailBannedPhrase) {
		t.Error("the banned words are still there")
	}
	if !Fixed(banned, qa.FailPacing) || !Fixed(banned, qa.FailTitleInvalid) {
		t.Error("pacing and title passed")
	}

	if Fixed(shortReport(), qa.FailWordcountLow) {
		t.Error("still too short")
	}
	if Fixed(longReport(), qa.FailWordcountLow) {
		t.Error("too long is not fixed for a plan that wanted more words, the word count check fails either way")
	}
	if Fixed(pacingReport(), qa.FailPacing) || Fixed(titleReport(), qa.FailTitleInvalid) {
		t.Error("pacing and title still fail")
	}
	good := qa.Script("Velvet Meadow", script.SampleScript(12), p5)
	for _, ft := range []qa.FailType{qa.FailWordcountLow, qa.FailBannedPhrase, qa.FailPacing, qa.FailTitleInvalid} {
		if !Fixed(good, ft) {
			t.Errorf("a passing report fixes everything, %s", ft)
		}
	}
	if Fixed(shortReport(), qa.FailType("SOMETHING_NEW")) {
		t.Error("an unknown failure type is fixed only when the whole report passes")
	}
}

func TestScorerWeightsFavourSuccessOverSpeed(t *testing.T) {
	if weightSuccessRate+weightAvgAttempts != 1 {
		t.Errorf("weights sum to %v, want 1", weightSuccessRate+weightAvgAttempts)
	}
	if weightSuccessRate <= weightAvgAttempts {
		t.Errorf("success rate weight %v must exceed attempts weight %v", weightSuccessRate, weightAvgAttempts)
	}
}
