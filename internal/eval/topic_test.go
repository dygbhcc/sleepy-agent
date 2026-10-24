package eval

import (
	"reflect"
	"strings"
	"testing"
)

func TestTopicWordsDropsFillerAndPlurals(t *testing.T) {
	got := TopicWords("The Quiet Library")
	if !reflect.DeepEqual(got, []string{"quiet", "library"}) {
		t.Fatalf("got %v", got)
	}
	if got := TopicWords("Amber Lanterns"); !reflect.DeepEqual(got, []string{"amber", "lantern"}) {
		t.Fatalf("got %v", got)
	}
	if got := TopicWords("A Lighthouse at Dusk"); !reflect.DeepEqual(got, []string{"lighthouse", "dusk"}) {
		t.Fatalf("got %v", got)
	}
	if got := TopicWords("moss moss Moss"); len(got) != 1 {
		t.Fatalf("repeats must collapse: %v", got)
	}
}

func TestTopicCoverageCountsWordsAndMentions(t *testing.T) {
	cov, of, n := TopicCoverage("Velvet Meadow", "The meadow sleeps. A meadow wind, and the Meadow again.")
	if cov != 1 || of != 2 || n != 3 {
		t.Fatalf("covered %d of %d, mentions %d", cov, of, n)
	}
	cov, of, n = TopicCoverage("Amber Lanterns", "One lantern glows amber, then two lanterns.")
	if cov != 2 || of != 2 || n != 3 {
		t.Fatalf("plural must match: covered %d of %d, mentions %d", cov, of, n)
	}
}

func TestSExceptionForShortWords(t *testing.T) {
	// "gas" and "bus" keep their s: only words longer than three letters lose one.
	if stem("gas") != "gas" || stem("moons") != "moon" || stem("moss") != "mos" {
		t.Fatalf("stem: %q %q %q", stem("gas"), stem("moons"), stem("moss"))
	}
}

func TestTopicConcerns(t *testing.T) {
	body := "The lighthouse stands above the dark water. " + strings.Repeat("Calm waves rest. ", 40)
	if c := TopicConcerns("A Lighthouse at Dusk", "Lighthouse Light", body); len(c) != 0 {
		t.Fatalf("unexpected %v", c)
	}
	c := TopicConcerns("The Quiet Library", "Lanterned Garden", "Soft paper rests on a wooden shelf.")
	if len(c) != 2 {
		t.Fatalf("want a script and a title concern, got %v", c)
	}
	if c := TopicConcerns("Velvet Meadow", "Silken Dawn", "A meadow at first light. "+strings.Repeat("Calm waves rest. ", 40)); len(c) != 1 || c[0] != "the title keeps none of the topic's words" {
		t.Fatalf("only the title fails: %v", c)
	}
	if c := TopicConcerns("Velvet Meadow", "", "A meadow. "+strings.Repeat("Calm waves rest. ", 40)); len(c) != 0 {
		t.Fatalf("no title to judge, got %v", c)
	}
	if c := TopicConcerns("the of", "x", "y"); c != nil {
		t.Fatalf("a topic with no meaningful words has nothing to check: %v", c)
	}
}

func TestTopicDensityIsMentionsPer100Words(t *testing.T) {
	body := strings.Repeat("moon ", 5) + strings.Repeat("calm ", 95)
	if d := TopicDensity("The Moon", body); d != 5 {
		t.Fatalf("density %.2f, want 5", d)
	}
	if TopicDensity("The Moon", "") != 0 {
		t.Fatal("no text, no density")
	}
}

func TestADenseTopicIsAConcernAndANormalOneIsNot(t *testing.T) {
	calm := strings.Repeat("moon ", 2) + strings.Repeat("calm ", 98) // 2 per 100
	if c := TopicConcerns("The Moon", "Moon Light", calm); len(c) != 0 {
		t.Fatalf("unexpected %v", c)
	}
	dense := strings.Repeat("moon ", 5) + strings.Repeat("calm ", 95) // 5 per 100
	c := TopicConcerns("The Moon", "Moon Light", dense)
	if len(c) != 1 || !strings.Contains(c[0], "per 100 words") {
		t.Fatalf("want a density concern, got %v", c)
	}
	edge := strings.Repeat("moon ", 4) + strings.Repeat("calm ", 96) // exactly 4: allowed
	if c := TopicConcerns("The Moon", "Moon Light", edge); len(c) != 0 {
		t.Fatalf("the limit itself is allowed: %v", c)
	}
}

func TestMetaSentencesAreTheNarratorTalkingAboutTheEpisode(t *testing.T) {
	body := "The episode returns to The Slow Moons. The moons rest above the quiet lake. In this video we drift on."
	if n := MetaSentences(body); n != 2 {
		t.Fatalf("got %d, want 2", n)
	}
	if n := MetaSentences("The moons rest. The story of the lake is old."); n != 0 {
		t.Fatalf("got %d, want 0", n)
	}
}
