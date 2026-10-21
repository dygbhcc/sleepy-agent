package eval

import (
	"math"
	"strings"
	"testing"

	"sleepy-agent/internal/script"
)

func near(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

// varied has 4 distinct sentences: 5 words, 5 words, 10 words and 22 words.
const varied = "Moss covers the old wall. Rain taps the tin roof. " +
	"A heron stands motionless where the shallow stream widens toward dusk. " +
	"Far below the ridge a lantern swings from a hook beside the door of a cottage, and its warm amber light spreads slowly across the damp grass."

func TestAssessSentenceShape(t *testing.T) {
	q := Assess(varied)
	if q.Sentences != 4 {
		t.Fatalf("sentences = %d", q.Sentences)
	}
	near(t, "ShortShare", q.ShortShare, 0.5)
	near(t, "LongShare", q.LongShare, 0.25)
	if q.RepeatedGramShare != 0 {
		t.Errorf("distinct sentences repeat %.2f of their 4-grams", q.RepeatedGramShare)
	}
	if q.UniqueWordRatio < 0.8 {
		t.Errorf("unique ratio %.2f for distinct text", q.UniqueWordRatio)
	}
}

func TestAssessEmpty(t *testing.T) {
	if q := Assess("  \n\n "); q != (Quality{}) {
		t.Errorf("%+v", q)
	}
}

func TestAssessSeesALoopedSentence(t *testing.T) {
	s := "The river moves slowly past the old stone bridge tonight."
	q := Assess(strings.Repeat(s+" ", 6))
	if q.RepeatedGramShare < 0.9 {
		t.Errorf("a sentence repeated 6 times has repeated share %.2f", q.RepeatedGramShare)
	}
	if q.UniqueWordRatio > 0.25 {
		t.Errorf("unique ratio %.2f", q.UniqueWordRatio)
	}
}

func TestEchoes(t *testing.T) {
	body := "Dew forming on cool morning grass. Later, dew forming on cool morning grass again, and the " +
		"MOSS GROWING ON ANCIENT STONES. Snow falling softly on sleeping fields."
	if got := Echoes(body, "Earthside"); got != 4 {
		t.Errorf("Earthside echoes = %d, want 4 (case and closing period ignored)", got)
	}
	if got := Echoes(body, "Cosmos"); got != 0 {
		t.Errorf("another style's lines are not echoed, got %d", got)
	}
	if got := Echoes(body, "nope"); got != 0 {
		t.Errorf("unknown style: %d", got)
	}
	if got := Echoes("", "Earthside"); got != 0 {
		t.Errorf("empty: %d", got)
	}
}

func TestImageryLinesIsACopy(t *testing.T) {
	a := script.ImageryLines("Earthside")
	if len(a) != 10 {
		t.Fatalf("%d lines", len(a))
	}
	a[0] = "changed"
	if script.ImageryLines("Earthside")[0] == "changed" {
		t.Error("ImageryLines leaked its backing array")
	}
	if script.ImageryLines("nope") != nil {
		t.Error("unknown style should return nil")
	}
}

func TestAppendCopiedAndNovel(t *testing.T) {
	orig := "Moss covers the old wall near the quiet garden. Rain taps the tin roof above the sleeping kitchen."
	copied := orig + "\n\n" + orig
	a, ok := Append(orig, copied)
	if !ok || a.AddedWords != 18 {
		t.Fatalf("%+v %v", a, ok)
	}
	near(t, "CopiedShare of a pasted copy", a.CopiedShare, 1)
	near(t, "NoveltyShare of a pasted copy", a.NoveltyShare, 0)

	fresh := orig + "\n\nA heron stands motionless where shallow streams widen toward distant mountains."
	a, ok = Append(orig, fresh)
	if !ok {
		t.Fatal("not ok")
	}
	near(t, "CopiedShare of new text", a.CopiedShare, 0)
	near(t, "NoveltyShare of new text", a.NoveltyShare, 1)
}

func TestAppendNeedsAPrefixAndSomethingAdded(t *testing.T) {
	if _, ok := Append("One two three four five.", "Different start entirely here. More"); ok {
		t.Error("full does not start with original")
	}
	if _, ok := Append("One two three four five.", "One two three four five.\n\n"); ok {
		t.Error("nothing was added")
	}
	if _, ok := Append("", "text"); ok {
		t.Error("empty original")
	}
}

func TestConcernsAtTheBoundary(t *testing.T) {
	th := Thresholds{MaxShortShare: 0.10, MaxLongShare: 0.10, MaxRepeatedGramShare: 0.05, MinUniqueWordRatio: 0.35, MaxCopiedShare: 0.10, MinNoveltyShare: 0.30}
	if c := th.Concerns(Quality{Words: 100, ShortShare: 0.10, LongShare: 0.10, RepeatedGramShare: 0.05, UniqueWordRatio: 0.35}); len(c) != 0 {
		t.Errorf("exactly at the limits is fine: %v", c)
	}
	c := th.Concerns(Quality{Words: 100, ShortShare: 0.11, LongShare: 0.11, RepeatedGramShare: 0.06, UniqueWordRatio: 0.34})
	if len(c) != 4 {
		t.Errorf("just over every limit: %v", c)
	}
	if c := th.Concerns(Quality{Words: 40, UniqueWordRatio: 0.1}); len(c) != 0 {
		t.Errorf("unique ratio is not judged under 50 words: %v", c)
	}
	if c := th.AdditionConcerns(Addition{CopiedShare: 0.10, NoveltyShare: 0.30, ShortShare: 0.10}); len(c) != 0 {
		t.Errorf("addition at the limits: %v", c)
	}
	if c := th.AdditionConcerns(Addition{CopiedShare: 0.2, NoveltyShare: 0.1, ShortShare: 0.2}); len(c) != 3 {
		t.Errorf("addition over every limit: %v", c)
	}
}

func sentenceOf(n int) string { return strings.TrimSpace(strings.Repeat("word ", n)) + "." }

func TestSentenceLengthBoundaries(t *testing.T) {
	// 7 words is short, 8 is not; 20 is fine, 21 is long.
	q := Assess(sentenceOf(7) + " " + sentenceOf(8) + " " + sentenceOf(20) + " " + sentenceOf(21))
	if q.Sentences != 4 {
		t.Fatalf("sentences = %d", q.Sentences)
	}
	near(t, "ShortShare", q.ShortShare, 0.25)
	near(t, "LongShare", q.LongShare, 0.25)
}
