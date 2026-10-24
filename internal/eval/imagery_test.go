package eval

import (
	"strings"
	"testing"
)

func TestAnchorMatchesAllowsShortEndings(t *testing.T) {
	for _, c := range []struct {
		word, anchor string
		want         bool
	}{
		{"fireflies", "firefl", true}, {"libraries", "librar", true}, {"rain", "rain", true}, {"rainy", "rain", true},
		{"rainfall", "rain", false}, {"train", "rain", false}, {"lakeside", "lake", false}, {"lakes", "lake", true},
	} {
		if got := anchorMatches(c.word, c.anchor); got != c.want {
			t.Errorf("anchorMatches(%q, %q) = %v, want %v", c.word, c.anchor, got, c.want)
		}
	}
}

const tour = "Dew rests. A lake glows. Moss grows. A river bends. Rain falls. Fireflies drift. Fog settles. Moonlight pools. Birch bark peels. Snow falls."

func TestAScriptTouchingEveryStockImageIsATour(t *testing.T) {
	used, of := StockImageryUsed("Velvet Meadow", tour, "Earthside")
	if used != 10 || of != 10 {
		t.Fatalf("used %d of %d", used, of)
	}
	if c := StockImageryConcern("Velvet Meadow", tour, "Earthside"); len(c) != 1 || !strings.Contains(c[0], "10 of 10") {
		t.Fatalf("concern %v", c)
	}
}

func TestAnAnchorThatIsATopicWordDoesNotCount(t *testing.T) {
	used, of := StockImageryUsed("Snow on Sleeping Fields", tour, "Earthside")
	if used != 9 || of != 9 {
		t.Fatalf("snow is the topic, so 9 of 9: got %d of %d", used, of)
	}
	// Plural and singular match each other: a topic of "Lakes" also frees "lake".
	if _, of := StockImageryUsed("Quiet Lakes", tour, "Earthside"); of != 9 {
		t.Fatalf("lakes frees lake: of = %d", of)
	}
}

func TestAFocusedScriptIsNotATour(t *testing.T) {
	body := "The lighthouse stands above the dark water. Its beam sweeps slowly. Fog drifts past. A lake of light rests below."
	used, of := StockImageryUsed("A Lighthouse at Dusk", body, "Earthside")
	if used != 2 || of != 10 {
		t.Fatalf("fog and lake only: used %d of %d", used, of)
	}
	if c := StockImageryConcern("A Lighthouse at Dusk", body, "Earthside"); len(c) != 0 {
		t.Fatalf("unexpected %v", c)
	}
}

func TestTheTourLimitIsNineTenths(t *testing.T) {
	if walksTheList(8, 10) || !walksTheList(9, 10) || !walksTheList(9, 9) || walksTheList(4, 4) {
		t.Fatal("limit: 9 of 10 is a tour, 8 of 10 is not, and fewer than 5 countable anchors say nothing")
	}
}

func TestUnknownStyleCountsNothing(t *testing.T) {
	if used, of := StockImageryUsed("x", tour, "Noir"); used != 0 || of != 0 {
		t.Fatalf("%d of %d", used, of)
	}
}
