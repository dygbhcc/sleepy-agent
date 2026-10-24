package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"sleepy-agent/internal/eval"
	"sleepy-agent/internal/judge"
	"sleepy-agent/internal/script"
)

const para1 = "Moss covers the old wall near the quiet garden tonight. Rain taps the tin roof above the sleeping kitchen. " +
	"A heron stands motionless where shallow streams widen toward distant mountains. Warm lanterns glow softly beside the cottage door tonight."
const para2 = "Dew forming on cool morning grass. Dew forming on cool morning grass again tonight. " +
	"Dew forming on cool morning grass near the gate. Dew forming on cool morning grass once more."

func fixstatsFile(final string, attempts string) string {
	return "arm: directed\nstatus: DONE\n\nattempts:\n" + attempts + "\nfinal script (what the run holds):\n\n" + final + "\n"
}

func TestParseFixstatsFileWithAContinuation(t *testing.T) {
	n := len(strings.Fields(para1))
	attempts := "1. first, " + itoa(n) + " words, WORDCOUNT_LOW WORDCOUNT_LOW: word count\n2. continue, 40 words, pass \n"
	s, ok := parse("x/01.md", fixstatsFile(para1+"\n\n"+para2, attempts))
	if !ok || s.arm != "directed" {
		t.Fatalf("%+v %v", s, ok)
	}
	if s.original != para1 {
		t.Errorf("original = %q, want the first paragraph", s.original)
	}
}

func TestParseWithoutAContinuationHasNoOriginal(t *testing.T) {
	s, ok := parse("x", fixstatsFile(para1, "1. first, 40 words, pass \n"))
	if !ok || s.original != "" {
		t.Fatalf("%+v %v", s, ok)
	}
}

func TestParseScriptstatsFileAndEmptyFiles(t *testing.T) {
	s, ok := parse("run-1.md", "title: Soft Lake\ntopic: Soft Lake\n\n"+para1+"\n")
	if !ok || s.body != para1 {
		t.Fatalf("%+v %v", s, ok)
	}
	if _, ok := parse("x", fixstatsFile("", "1. first, 0 words, truncated script\n")); ok {
		t.Error("a run with no script text is not a sample")
	}
	if _, ok := parse("x", "something else"); ok {
		t.Error("unknown format")
	}
}

func TestFirstWordsNeedsAParagraphBoundary(t *testing.T) {
	if _, ok := firstWords(para1+"\n\n"+para2, 5); ok {
		t.Error("5 words ends inside a paragraph")
	}
	if _, ok := firstWords(para1, 0); ok {
		t.Error("zero words")
	}
}

func TestReportFlagsEchoesAndRecycledContinuations(t *testing.T) {
	good, _ := parse("good.md", fixstatsFile(para1, "1. first, 40 words, pass \n"))
	echo, _ := parse("echo.md", fixstatsFile(para1+"\n\n"+para2+"\n\n"+para2+"\n\n"+para2, "1. first, 40 words, pass \n"))
	n := len(strings.Fields(para1))
	rec, _ := parse("recycled.md", fixstatsFile(para1+"\n\n"+para1, "1. first, "+itoa(n)+" words, WORDCOUNT_LOW x\n2. continue, 40 words, pass \n"))
	var out bytes.Buffer
	report(&out, []sample{good, echo, rec}, eval.DefaultThresholds, "Earthside")
	s := out.String()
	for _, want := range []string{"good.md", "verbatim copies of the prompt's imagery lines", "of 3 scripts have at least one concern",
		"Text added by a continuation", "recycled.md", "of the added content words are new", "1 of 1 continuations"} {
		if !strings.Contains(s, want) {
			t.Errorf("report misses %q:\n%s", want, s)
		}
	}
	if !strings.Contains(lineWith(s, "good.md"), "| none |") {
		t.Errorf("the good script should have no concerns: %s", lineWith(s, "good.md"))
	}
}

func TestLoadReadsAFolder(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "01-directed.md"), []byte(fixstatsFile(para1, "1. first, 40 words, pass \n")), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "02-empty.md"), []byte(fixstatsFile("", "1. first, 0 words, truncated x\n")), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignored"), 0o644)
	got, err := load([]string{dir})
	if err != nil || len(got) != 1 {
		t.Fatalf("%d samples, err %v", len(got), err)
	}
}

func lineWith(s, sub string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, sub) {
			return l
		}
	}
	return ""
}

func itoa(n int) string { return strconv.Itoa(n) }

type fakeReviewer struct {
	byBody map[string]judge.Verdict
	errOn  string
	seen   []string
}

func (f *fakeReviewer) Review(_ context.Context, topic, _ string, body string) (judge.Verdict, error) {
	f.seen = append(f.seen, topic)
	if f.errOn != "" && strings.Contains(body, f.errOn) {
		return judge.Verdict{}, errors.New("provider down")
	}
	return f.byBody[body], nil
}

func mustParse(t *testing.T, name, topic, body string) sample {
	t.Helper()
	text := "arm: blind\ntopic: " + topic + "\nstatus: DONE\n\nattempts:\n1. first, 40 words, pass \n\nfinal script (what the run holds):\n\n" + body + "\n"
	s, ok := parse(name, text)
	if !ok {
		t.Fatal("not parsed")
	}
	return s
}

func TestParseKeepsTheTopic(t *testing.T) {
	if s := mustParse(t, "a", "The Quiet Library", para1); s.topic != "The Quiet Library" {
		t.Errorf("topic %q", s.topic)
	}
}

func TestJudgeAllAndReport(t *testing.T) {
	a := mustParse(t, "a.md", "Lake", para1)
	b := mustParse(t, "b.md", "Library", para2)
	c := mustParse(t, "c.md", "Moon", para1+" extra")
	rv := &fakeReviewer{
		byBody: map[string]judge.Verdict{
			a.body: {Calm: 5, Coherence: 5, Variety: 4, Narration: 5, Pass: true, MinScore: 3},
			b.body: {Calm: 4, Coherence: 1, Variety: 2, Narration: 4, Pass: false, MinScore: 3, Reason: "forest images in a library", Evidence: "The river bends"},
		},
		errOn: "extra",
	}
	vs := judgeAll(context.Background(), []sample{a, b, c}, rv, 0)
	if strings.Join(rv.seen, ",") != "Lake,Library,Moon" {
		t.Errorf("topics seen %v", rv.seen)
	}
	var out bytes.Buffer
	judgeReport(&out, []sample{a, b, c}, vs, 3)
	for _, want := range []string{"| a.md | 5 | 5 | 4 | 5 | pass |", "| b.md | 4 | 1 | 2 | 4 | would reject | forest images in a library | The river bends |",
		"| c.md | | | | | error | provider down |", "1 pass, 1 would reject, 1 errors"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report misses %q:\n%s", want, out.String())
		}
	}
}

func TestLabelsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "labels.csv")
	a := mustParse(t, "a.md", "", para1)
	b := mustParse(t, "b.md", "", para2)
	if _, err := readLabels(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file: %v", err)
	}
	if err := writeLabelTemplate(path, []sample{a, b}); err != nil {
		t.Fatal(err)
	}
	got, err := readLabels(path)
	if err != nil || len(got) != 0 {
		t.Fatalf("a template has no labels yet: %v %v", got, err)
	}
	if err := os.WriteFile(path, []byte("a.md, GOOD\nb.md,bad\nc.md,maybe\nbroken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ = readLabels(path)
	if got["a.md"] != "good" || got["b.md"] != "bad" || len(got) != 2 {
		t.Fatalf("labels %v", got)
	}
}

func TestAgreementCountsBothCheckers(t *testing.T) {
	fine := mustParse(t, "fine.md", "", para1)
	echo := mustParse(t, "echo.md", "", para1+"\n\n"+para2+"\n\n"+para2+"\n\n"+para2)
	other := mustParse(t, "other.md", "", strings.Replace(para1, "Moss", "Ivy", 1))
	samples := []sample{fine, echo, other}
	labels := map[string]string{"fine.md": "good", "echo.md": "bad", "other.md": "bad"}
	vs := verdicts{}
	set := func(name string, pass bool) {
		vs[name] = struct {
			v   judge.Verdict
			err error
		}{v: judge.Verdict{Pass: pass}}
	}
	set("fine.md", true)   // judge agrees
	set("echo.md", true)   // judge disagrees: human says bad
	set("other.md", false) // judge agrees

	var out bytes.Buffer
	agreementReport(&out, samples, labels, eval.DefaultThresholds, "Earthside", vs)
	s := out.String()
	// metrics: fine not flagged (good, agrees), echo flagged (bad, agrees), other not flagged (human bad, disagrees)
	for _, want := range []string{"3 of 3 scripts labelled", "| metrics (concerns or echoes means bad) | 2 | 3 |", "| shadow judge | 2 | 3 |",
		"other.md: human bad, metrics good", "echo.md: human bad, judge good"} {
		if !strings.Contains(s, want) {
			t.Errorf("report misses %q:\n%s", want, s)
		}
	}
}

func TestAgreementWithNothingLabelled(t *testing.T) {
	var out bytes.Buffer
	agreementReport(&out, []sample{mustParse(t, "a.md", "", para1)}, map[string]string{}, eval.DefaultThresholds, "Earthside", nil)
	if !strings.Contains(out.String(), "0 of 1 scripts labelled") || strings.Contains(out.String(), "shadow judge") {
		t.Errorf("%s", out.String())
	}
}

func TestAgreementTreatsEchoesAsAConcernOnTheirOwn(t *testing.T) {
	// Every sentence is long enough, so only the verbatim copies can flag it
	// once the repetition limits are off: 4 imagery lines in each of 3 paragraphs.
	const lines = "Tonight the dew forming on cool morning grass settles across the quiet meadow. " +
		"Slowly the moss growing on ancient stones drinks the cool evening air around it. " +
		"Far away the fireflies drifting in warm dusk air blink above the sleeping fields. " +
		"Gently the snow falling softly on sleeping fields covers every hedge and fence post."
	echo := mustParse(t, "echo.md", "", lines+"\n\n"+lines+"\n\n"+lines)
	if n := eval.Echoes(echo.body, "Earthside"); n != 12 {
		t.Fatalf("test setup: %d echoes, want 12", n)
	}
	th := eval.DefaultThresholds
	th.MaxRepeatedGramShare, th.MinUniqueWordRatio = 1, 0
	var out bytes.Buffer
	agreementReport(&out, []sample{echo}, map[string]string{"echo.md": "bad"}, th, "Earthside", nil)
	if !strings.Contains(out.String(), "| metrics (concerns or echoes means bad) | 1 | 1 |") {
		t.Errorf("12 echoes should flag the script:\n%s", out.String())
	}
}

func TestAgreementLeavesJudgeErrorsOut(t *testing.T) {
	a := mustParse(t, "a.md", "", para1)
	vs := verdicts{"a.md": struct {
		v   judge.Verdict
		err error
	}{err: errors.New("provider down")}}
	var out bytes.Buffer
	agreementReport(&out, []sample{a}, map[string]string{"a.md": "good"}, eval.DefaultThresholds, "Earthside", vs)
	if !strings.Contains(out.String(), "| shadow judge | 0 | 0 |") {
		t.Errorf("a judge error is not a verdict:\n%s", out.String())
	}
}

func TestEchoesAreCountedAgainstTheStyleTheScriptWasWrittenUnder(t *testing.T) {
	cosmos := script.ImageryLines("Cosmos")[0]
	body := strings.Repeat(cosmos+" ", 12)
	text := "title: Soft Lake\ntopic: Soft Lake\nstyle: Cosmos\n\n" + body + "\n"
	s, ok := parse("c.md", text)
	if !ok || s.style != "Cosmos" {
		t.Fatalf("parse: ok %v style %q", ok, s.style)
	}
	var out bytes.Buffer
	report(&out, []sample{s}, eval.DefaultThresholds, "Earthside") // the default must not win
	if !strings.Contains(out.String(), "verbatim copies of the prompt's imagery lines") {
		t.Fatalf("a Cosmos script pasting Cosmos lines must be flagged even when the default style is Earthside:\n%s", out.String())
	}
}

func TestAFileWithoutAStyleFallsBackToTheDefault(t *testing.T) {
	s := mustParse(t, "a.md", "", para1)
	if s.style != "" || s.styleOr("Myth") != "Myth" {
		t.Fatalf("style %q", s.style)
	}
}

func TestReportShowsWhetherTheTopicAndTheTitleCarryTheTopic(t *testing.T) {
	on, _ := parse("on.md", "title: Quiet Library\ntopic: The Quiet Library\n\nThe library rests in a quiet hush.\n")
	off, _ := parse("off.md", "title: Lanterned Garden\ntopic: The Quiet Library\n\nSoft paper rests on a shelf.\n")
	var out bytes.Buffer
	report(&out, []sample{on, off}, eval.DefaultThresholds, "Earthside")
	got := out.String()
	if l := lineWith(got, "on.md"); !strings.Contains(l, "| 2/2 (2) | yes |") {
		t.Errorf("on-topic row: %s", l)
	}
	l := lineWith(got, "off.md")
	if !strings.Contains(l, "| 0/2 (0) | no |") || !strings.Contains(l, "none of the topic's words appear in the script") || !strings.Contains(l, "the title keeps none of the topic's words") {
		t.Errorf("off-topic row: %s", l)
	}
}
