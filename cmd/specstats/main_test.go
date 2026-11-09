package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sleepy-agent/internal/eval"
)

func TestSettleMarksDisagreeingRepeatsUnresolved(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"B", "B", "B"}, "B"},
		{[]string{"G", "B", "G"}, "U"},
		{[]string{"G", "E", "G"}, "E"},
		{nil, "E"},
	}
	for _, c := range cases {
		if got := settle(c.in); got != c.want {
			t.Errorf("settle(%v) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestClassOf(t *testing.T) {
	if classOf(eval.VerdictGoodSmallEdit) != clsGood || classOf(eval.VerdictBad) != clsBad || classOf(eval.VerdictNeedsReview) != clsReview {
		t.Fatal("verdict classes are wrong")
	}
}

func TestTallyCountsLetThroughAndFalseRejects(t *testing.T) {
	var ty tally
	ty.add(label{good: false}, clsGood)
	ty.add(label{good: false}, clsBad)
	ty.add(label{good: false}, clsReview)
	ty.add(label{good: true}, clsBad)
	ty.add(label{good: true}, clsGood)
	if ty.badPassed != 1 || ty.badRejected != 1 || ty.badOther != 1 || ty.goodRejected != 1 || ty.goodPassed != 1 {
		t.Fatalf("tally %+v", ty)
	}
	if !strings.Contains(ty.line("x"), "1 of 3") {
		t.Fatalf("line %q", ty.line("x"))
	}
}

func TestParseReadsTitleTopicAndBody(t *testing.T) {
	s, ok := parse("d/run-1.md", "title: T\ntopic: Saturn\nstyle: Earthside\n\nOne. Two.\n")
	if !ok || s.title != "T" || s.topic != "Saturn" || s.body != "One. Two." {
		t.Fatalf("%+v %v", s, ok)
	}
}

func TestNaturalSortPutsRun2BeforeRun10(t *testing.T) {
	if natural("d/run-2.md") >= natural("d/run-10.md") {
		t.Fatal("run-2 must sort first")
	}
}

func TestSelectedMatchesPartsOfNames(t *testing.T) {
	if !selected("d/run-4.md", "") || !selected("strict-a/run-4.md", "v3-b/run-4, strict-a/run-4") {
		t.Fatal("expected a match")
	}
	if selected("strict-a/run-5.md", "strict-a/run-4") {
		t.Fatal("unexpected match")
	}
}

func TestReadLabelsAcceptsScoredAndOldFormats(t *testing.T) {
	path := filepath.Join(t.TempDir(), "l.csv")
	text := "a,good,by-claude\nb,publish,5,4,5,5,nice\nc,reject,1,2,3,4,insects\nd,small-edit,3,3,3,3\ne,maybe\n"
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := readLabels(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 4 || !m["a"].good || !m["a"].byClaude || m["a"].scores != nil {
		t.Fatalf("old format: %+v", m)
	}
	if !m["b"].good || len(m["b"].scores) != 4 || m["b"].byClaude {
		t.Fatalf("publish: %+v", m["b"])
	}
	if m["c"].good || m["c"].scores[0] != 1 || !m["d"].good {
		t.Fatalf("reject and small-edit: %+v %+v", m["c"], m["d"])
	}
}
