package script

import (
	"reflect"
	"strings"
	"testing"

	"sleepy-agent/internal/policy"
)

var loose = policy.Policy{MinWords: 1, MaxWords: 100000, TargetWords: 650}

func TestCleanScriptHasNoViolations(t *testing.T) {
	r := Measure(cleanScript(12), loose)
	if !r.Clean() {
		t.Fatalf("want clean, got %v", r.Violations)
	}
	if r.Paragraphs != 12 || r.Sentences != 48 || r.Words != 12*4*12 {
		t.Fatalf("counts: %+v", r)
	}
}

func TestEachRuleIsDetectedOnItsOwn(t *testing.T) {
	base := cleanScript(3)
	withFirstSentence := func(old, repl string) string {
		return strings.Replace(base, old, repl, 1)
	}
	first := "The slow river 0 bends through the quiet meadow part 1 tonight."

	cases := []struct {
		name string
		text string
		want []string
	}{
		{"banned word", withFirstSentence(first, "The slow river 0 bends through the quiet meadow without any panic tonight."), []string{BannedWord}},
		{"banned phrase", withFirstSentence(first, "The slow river 0 bends through the quiet meadow, never fade away tonight."), []string{BannedWord}},
		{"exclamation", withFirstSentence(first, "The slow river 0 bends through the quiet meadow part 1 tonight!"), []string{Exclamation}},
		{"ellipsis", withFirstSentence(first, "The slow river 0 bends through the quiet meadow part 1 tonight..."), []string{Decoration}},
		{"em dash", withFirstSentence(first, "The slow river 0 bends — through the quiet meadow part 1 tonight."), []string{Decoration}},
		{"heading", "# The Quiet Forest\n\n" + base, []string{Markdown, SentenceLength, ParagraphShape}},
		{"bullet", base + "\n\n- a stray bullet line", []string{Markdown, SentenceLength, ParagraphShape}},
		{"all caps", withFirstSentence(first, "The slow river 0 bends through the QUIET meadow part 1 tonight."), []string{AllCaps}},
		{"short sentence", withFirstSentence(first, "Rest now."), []string{SentenceLength}},
		{"repeated sentence", strings.Replace(base, "part 2 tonight.", "part 1 tonight.", 1), []string{RepeatedSentence}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Measure(c.text, loose).Violations
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("violations %v, want %v", got, c.want)
			}
		})
	}
}

func TestBannedWordsMatchWholeWordsOnly(t *testing.T) {
	text := strings.Replace(cleanScript(1), "quiet meadow", "warm meadow of swarm", 1)
	if r := Measure(text, loose); len(r.BannedFound) != 0 {
		t.Fatalf("warm and swarm must not match war: %v", r.BannedFound)
	}
	text = strings.Replace(cleanScript(1), "quiet meadow", "meadow of WAR and Fear", 1)
	if got := Measure(text, loose).BannedFound; !reflect.DeepEqual(got, []string{"fear", "war"}) {
		t.Fatalf("case insensitive match, sorted and unique, got %v", got)
	}
}

func TestWordLimits(t *testing.T) {
	p := policy.Policy{MinWords: 100, MaxWords: 200}
	if got := Measure(cleanScript(1), p).Violations; !reflect.DeepEqual(got, []string{TooShort}) {
		t.Fatalf("48 words under a minimum of 100: %v", got)
	}
	if got := Measure(cleanScript(6), p).Violations; !reflect.DeepEqual(got, []string{TooLong}) {
		t.Fatalf("288 words over a maximum of 200: %v", got)
	}
	if got := Measure(cleanScript(3), p).Violations; len(got) != 0 {
		t.Fatalf("144 words is inside 100 to 200: %v", got)
	}
}

func TestDecimalPointsDoNotSplitSentences(t *testing.T) {
	got := sentences("The lake is 2.5 meters deep and the water is very calm tonight. Rest now.")
	if len(got) != 2 {
		t.Fatalf("want 2 sentences, got %q", got)
	}
}

func TestEmptyTextIsTooShortAndNothingElse(t *testing.T) {
	r := Measure("", policy.ForDuration(5))
	if !reflect.DeepEqual(r.Violations, []string{TooShort}) {
		t.Fatalf("got %v", r.Violations)
	}
}

func TestBannedWordInsideALongerWordIsNotAMatch(t *testing.T) {
	// "begun" ends with gun, "warm" starts with war, "wars" and "killed" are other forms.
	text := strings.Replace(cleanScript(1), "quiet meadow", "begun warm meadow", 1)
	if r := Measure(text, loose); len(r.BannedFound) != 0 {
		t.Fatalf("got %v", r.BannedFound)
	}
}

func TestFiveSentenceParagraphAndLongSentenceAreBothCaught(t *testing.T) {
	five := cleanParagraph(0) + " One more calm line for the meadow tonight friend."
	got := Measure(five, loose).Violations
	if !reflect.DeepEqual(got, []string{ParagraphShape}) {
		t.Fatalf("5 sentences: got %v", got)
	}
	long := "The slow river bends through the quiet meadow and then it keeps drifting past the old stone wall toward the sleeping hills."
	para := strings.Join([]string{long, "Rest now in the quiet meadow tonight my friend.", "The slow river bends through the quiet meadow tonight.", "The slow river bends through the quiet meadow again."}, " ")
	if got := Measure(para, loose).Violations; !reflect.DeepEqual(got, []string{SentenceLength}) {
		t.Fatalf("long sentence: got %v", got)
	}
}

func TestSampleScriptFollowsEveryRule(t *testing.T) {
	rep := Measure(SampleScript(12), policy.ForDuration(5))
	if !rep.Clean() {
		t.Fatalf("the sample must be clean, got %v", rep.Violations)
	}
	if rep.Words != 624 {
		t.Fatalf("words = %d, want 624", rep.Words)
	}
}

func TestBannedCountsListsWholeWordsInListOrder(t *testing.T) {
	hits := BannedCounts("A warm day. War came, and war left. He did not wake up; the Fear stayed. Suddenly.")
	want := []BannedHit{{"suddenly", 1}, {"war", 2}, {"fear", 1}, {"wake up", 1}}
	if len(hits) != len(want) {
		t.Fatalf("hits = %+v, want %+v", hits, want)
	}
	for i := range want {
		if hits[i] != want[i] {
			t.Fatalf("hits = %+v, want %+v", hits, want)
		}
	}
}
