package eval

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// clean builds n distinct calm sentences with no filler tail and no shared
// content words, so no paragraph overlap or repeated opening appears by chance.
func clean(n int) []string {
	var out []string
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf("Quiet%d thing%d moves near hill%d.", i, i, i))
	}
	return out
}

func find(r SpecReport, id string) (Finding, bool) {
	for _, f := range r.Findings {
		if f.ID == id {
			return f, true
		}
	}
	return Finding{}, false
}

func TestSpecSentencesSplitsAtPunctuationAndNewlines(t *testing.T) {
	got := SpecSentences("One. Two!  \nThree? Four")
	if !reflect.DeepEqual(got, []string{"One.", "Two!", "Three?", "Four"}) {
		t.Fatalf("got %q", got)
	}
}

func TestSmallEditLimitIsFiveOrTenPercent(t *testing.T) {
	for n, want := range map[int]int{10: 5, 48: 5, 60: 6, 100: 10} {
		if got := SmallEditLimit(n); got != want {
			t.Fatalf("SmallEditLimit(%d) = %d, want %d", n, got, want)
		}
	}
}

func TestCleanScriptHasNoFindings(t *testing.T) {
	r := CheckSpec("", "", strings.Join(clean(48), "\n"))
	if r.Verdict != VerdictGood || len(r.Findings) != 0 {
		t.Fatalf("clean script: %+v", r)
	}
}

func TestFillerTailRejectsFromSixtyPercentAndIsAdvisoryFromATenth(t *testing.T) {
	build := func(filler int) string {
		s := clean(48)
		for i := 0; i < filler; i++ {
			s[i] = strings.TrimSuffix(s[i], ".") + " softly."
		}
		return strings.Join(s, "\n")
	}
	r28 := CheckSpec("", "", build(28))
	if f, _ := find(r28, "H1"); f.Reject || !f.Warn || !f.Advisory || r28.Verdict != VerdictGoodSmallEdit {
		t.Fatalf("28 of 48 is 58 percent: a warning that does not count toward the combined rule: %+v %s", f, r28.Verdict)
	}
	r29 := CheckSpec("", "", build(29))
	if f, _ := find(r29, "H1"); !f.Reject || r29.Verdict != VerdictBad {
		t.Fatalf("29 of 48 is 60 percent and must reject: %+v %s", f, r29.Verdict)
	}
	r := CheckSpec("", "", build(5))
	if f, ok := find(r, "H1"); !ok || f.Reject || !f.Warn || r.Verdict != VerdictGoodSmallEdit {
		t.Fatalf("5 of 48 is a warning and a small edit: %+v %s", f, r.Verdict)
	}
	if _, ok := find(CheckSpec("", "", build(4)), "H1"); ok {
		t.Fatal("4 of 48 is under 10 percent and not reported")
	}
}

func TestFillerTailNeedsTheWholeWord(t *testing.T) {
	for _, s := range []string{"The tide moves nowhere.", "A path leads to the glow of the shore.", "The night is long."} {
		if hasFillerTail(s) {
			t.Fatalf("%q is not a filler tail", s)
		}
	}
	for _, s := range []string{"The tide moves in quietness.", "Stars shine tonight.", "Hush settles in silence."} {
		if !hasFillerTail(s) {
			t.Fatalf("%q is a filler tail", s)
		}
	}
}

func TestDanglingSentencesRejectFromSix(t *testing.T) {
	build := func(n int) string {
		s := clean(48)
		for i := 0; i < n; i++ {
			s[i] = fmt.Sprintf("The wind number %d carries a soft blanket of.", i)
		}
		return strings.Join(s, "\n")
	}
	if f, _ := find(CheckSpec("", "", build(6)), "H2"); !f.Reject {
		t.Fatalf("6 dangling sentences reject: %+v", f)
	}
	if f, _ := find(CheckSpec("", "", build(5)), "H2"); f.Reject || !f.Warn {
		t.Fatalf("5 dangling sentences warn: %+v", f)
	}
}

func TestDuplicateSentencesWarnUnderSixAndKeepQuote(t *testing.T) {
	s := clean(48)
	s[10] = "A distant brook hums a lullaby beneath silvered stones gentle."
	s[20] = s[10]
	r := CheckSpec("", "", strings.Join(s, "\n"))
	f, ok := find(r, "H3a")
	if !ok || f.Reject || f.Count != 1 || !strings.Contains(f.Quote, "distant brook") || r.Verdict != VerdictGoodSmallEdit {
		t.Fatalf("one duplicate is a small edit: %+v %s", f, r.Verdict)
	}
}

func TestCycleOfSentencesRejects(t *testing.T) {
	s := clean(48)
	for i := 0; i < 3; i++ {
		s[30+i] = s[i]
	}
	if f, ok := find(CheckSpec("", "", strings.Join(s, "\n")), "H3c"); !ok || !f.Reject {
		t.Fatalf("a 3 sentence run that appears twice must reject: %+v", f)
	}
}

func TestSameOpeningInFortyPercentOfParagraphsRejects(t *testing.T) {
	var paras []string
	for i := 0; i < 10; i++ {
		open := fmt.Sprintf("Ripple%d forms near the quiet shore", i)
		if i < 4 {
			open = "The soft light drifts across"
		}
		paras = append(paras, fmt.Sprintf("%s item%d. Another%d line%d here.", open, i, i, i))
	}
	if f, ok := find(CheckSpec("", "", strings.Join(paras, "\n\n")), "H3b"); !ok || !f.Reject {
		t.Fatalf("4 of 10 paragraphs open alike: %+v", f)
	}
}

func TestTwoParagraphsSharingMostContentWordsReject(t *testing.T) {
	a := "Calm cosmic winds drift across velvet dark nebula haze while slow moons glide."
	b := "Slow moons glide while nebula haze and velvet dark drift as calm cosmic winds."
	paras := []string{a, b}
	for i := 0; i < 4; i++ {
		paras = append(paras, fmt.Sprintf("Unrelated%d orchard%d harbor%d thicket%d bridge%d.", i, i, i, i, i))
	}
	if f, ok := find(CheckSpec("", "", strings.Join(paras, "\n\n")), "H3d"); !ok || !f.Reject {
		t.Fatalf("paragraph overlap must reject: %+v", f)
	}
}

func TestTopicKeys(t *testing.T) {
	for topic, want := range map[string][]string{
		"Rings of Saturn":         {"saturn"},
		"A Lighthouse at Dusk":    {"lighthouse"},
		"Snow on Sleeping Fields": {"snow"},
		"Moss and Still Water":    {stem("moss"), "water"},
		"The Quiet Library":       {"library"},
		"Amber Lanterns":          {"lantern"},
		"Velvet Meadow":           {"meadow"},
	} {
		if got := topicKeys(topic); !reflect.DeepEqual(got, want) {
			t.Errorf("topicKeys(%q) = %v, want %v", topic, got, want)
		}
	}
}

func TestMissingTopicNounIsACandidateForTheJudgeNotARejection(t *testing.T) {
	r := CheckSpec("Rings of Saturn", "Silken Rings", strings.Join(clean(48), "\n"))
	f, ok := find(r, "H4")
	if !ok || f.Reject || !f.Warn || f.Count != 0 {
		t.Fatalf("H4 must warn only: %+v", f)
	}
	if r.Verdict == VerdictBad {
		t.Fatal("a missing noun alone must not reject: a library can be told without the word")
	}
}

func TestPossessiveCountsAsTheNoun(t *testing.T) {
	s := clean(48)
	s[1] = "The lighthouse's beam sweeps the water."
	s[2] = "A lighthouse’s lamp glows."
	if _, ok := find(CheckSpec("A Lighthouse at Dusk", "t", strings.Join(s, "\n")), "H4"); ok {
		t.Fatal("two possessive mentions are two mentions")
	}
}

func TestMetaAndNarratorInstructionLeakReject(t *testing.T) {
	for _, line := range []string{"The episode returns to The Slow Moons.", "Very quiet closing stillness settles, dim light steady."} {
		s := clean(48)
		s[40] = line
		if f, ok := find(CheckSpec("", "", strings.Join(s, "\n")), "H8"); !ok || !f.Reject {
			t.Fatalf("%q must reject: %+v", line, f)
		}
	}
}

func TestTitleWithoutATopicWordIsOnlyAWarning(t *testing.T) {
	r := CheckSpec("Velvet Meadow", "Moonlit Hills", strings.Join(append(clean(46), "The meadow rests.", "The meadow sleeps."), "\n"))
	if f, ok := find(r, "T1"); !ok || f.Reject || !f.Warn {
		t.Fatalf("title warning: %+v", f)
	}
	if r.Verdict == VerdictBad {
		t.Fatal("a title can never reject")
	}
}

func TestSmallSlipsOfDifferentKindsAddUpToBad(t *testing.T) {
	s := clean(48)
	for i := 0; i < 3; i++ { // 3 dangling (a warning)
		s[i] = fmt.Sprintf("The wind number %d carries a soft blanket of.", i)
	}
	code := CheckSpec("", "", strings.Join(s, "\n"))
	judge := Finding{ID: "H2b", Count: 4, Warn: true, Sentences: []int{3, 4, 5, 6}, Quote: "stones gentle"}
	r := Decide(48, append(code.Findings, judge))
	if r.Verdict != VerdictBad || r.Rejecting[len(r.Rejecting)-1] != "combined" {
		t.Fatalf("7 sentences to fix is more than a small edit of 5: %+v", r)
	}
}

func TestUnsettlingWordsAreAdvisoryAndWholeWord(t *testing.T) {
	s := clean(48)
	s[4] = "A lone beetle crawls across the leaf."
	s[9] = "Soft light caresses each blade of grass."
	r := CheckSpec("", "", strings.Join(s, "\n"))
	f, ok := find(r, "H11")
	if !ok || f.Reject || !f.Advisory || f.Count != 1 {
		t.Fatalf("one unsettling word, advisory only: %+v", f)
	}
	if r.Verdict != VerdictGoodSmallEdit {
		t.Fatalf("an advisory warning does not reject: %s", r.Verdict)
	}
}

func TestDecideAddsJudgeFindingsToTheCodeChecks(t *testing.T) {
	r := Decide(48, []Finding{{ID: "H5a", Count: 1, Reject: true, Quote: "snow falls beside fireflies"}})
	if r.Verdict != VerdictBad || !reflect.DeepEqual(r.Rejecting, []string{"H5a"}) {
		t.Fatalf("%+v", r)
	}
	r = Decide(48, []Finding{{ID: "H7", Count: 2, Warn: true, Sentences: []int{3, 9}, Quote: "A fox watches."}})
	if r.Verdict != VerdictGoodSmallEdit || len(r.FixQuotes) != 1 {
		t.Fatalf("%+v", r)
	}
	if r := Decide(48, nil); r.Verdict != VerdictGood {
		t.Fatalf("%+v", r)
	}
}
