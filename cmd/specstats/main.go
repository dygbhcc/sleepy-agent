// Command specstats measures saved scripts against docs/quality-spec.md.
//
// For each script it runs the code checks (internal/eval.CheckSpec) and, when
// GROQ_API_KEY and JUDGE_MODELS are set, the judge checklist
// (internal/judge.Checklist) once per model and repeat. Code and checklist
// findings are merged with eval.Decide, so the spec's combined rule decides.
// Nothing here accepts or rejects a run (rule 11): it prints verdicts and then
// compares them with a human's labels.
//
// Usage: go run ./cmd/specstats DIR...
//
// Environment:
//
//	LABELS          label file, default out/labels.csv (path,good|bad,tags).
//	                Names are as evalstats prints them: dir/run-N.md. The tag
//	                by-claude marks a label written by Claude; those are
//	                reported apart from the owner's.
//	JUDGE_MODELS    for example "openai/gpt-oss-120b:low,qwen/qwen3.8-27b".
//	                Without it, or without GROQ_API_KEY, only code checks run.
//	REPEAT          times each model checks each script, default 1. With 3 or
//	                more a script whose verdicts disagree is "unresolved".
//	MODE            owner_reviews (default) or autonomous: what unsure means.
//	CONFIRM         "presence": the confirm pass keeps a defect when it also says
//	                it is present, even with other quotes. Default: it must repeat
//	                one of the first pass's quotes. A hypothesis, not yet measured.
//	VERBOSE=1       print what the model answered for each defect (stderr).
//	ALL=1           also check scripts that have no label.
//	ONLY            comma separated parts of script names; only those scripts
//	                are checked (for example "strict-a/run-4,v3-b/run-4").
//
// Scored labels: path,publish|small-edit|reject,s1,s2,s3,s4,tags with four
// scores from 1 to 5 (disturbing, repetition, coherence, concept; 5 is no
// problem). publish and small-edit count as good, reject as bad.
//
//	JUDGE_MAX_TOKENS, SCRIPT_PAUSE_SECONDS   as in cmd/evalstats.
package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"sleepy-agent/internal/eval"
	"sleepy-agent/internal/judge"
	"sleepy-agent/internal/llm"
)

type sample struct{ name, topic, title, body string }

type label struct {
	good     bool
	byClaude bool
	// scores are the owner's four 1 to 5 scores (disturbing, repetition,
	// coherence, concept); nil in the older good or bad files.
	scores []int
}

var (
	topicRe = regexp.MustCompile(`(?m)^topic: (.+)$`)
	titleRe = regexp.MustCompile(`(?m)^title: (.+)$`)
)

const finalMarker = "final script (what the run holds):"

func parse(name, text string) (sample, bool) {
	s := sample{name: name}
	if m := topicRe.FindStringSubmatch(text); m != nil {
		s.topic = strings.TrimSpace(m[1])
	}
	if m := titleRe.FindStringSubmatch(text); m != nil {
		s.title = strings.TrimSpace(m[1])
	}
	switch {
	case strings.Contains(text, finalMarker):
		s.body = strings.TrimSpace(text[strings.Index(text, finalMarker)+len(finalMarker):])
	case strings.HasPrefix(text, "title:"):
		if i := strings.Index(text, "\n\n"); i >= 0 {
			s.body = strings.TrimSpace(text[i:])
		}
	}
	return s, s.body != ""
}

func load(dirs []string) ([]sample, error) {
	var out []sample
	for _, d := range dirs {
		files, err := filepath.Glob(filepath.Join(d, "*.md"))
		if err != nil {
			return nil, err
		}
		sort.Slice(files, func(i, j int) bool { return natural(files[i]) < natural(files[j]) })
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				return nil, err
			}
			if s, ok := parse(filepath.Join(filepath.Base(d), filepath.Base(f)), string(b)); ok {
				out = append(out, s)
			}
		}
	}
	return out, nil
}

// selected reports whether name matches one of the comma separated parts in
// only. An empty only selects everything.
func selected(name, only string) bool {
	if strings.TrimSpace(only) == "" {
		return true
	}
	for _, part := range strings.Split(only, ",") {
		if p := strings.TrimSpace(part); p != "" && strings.Contains(name, p) {
			return true
		}
	}
	return false
}

var numRe = regexp.MustCompile(`\d+`)

// natural sorts run-2 before run-10.
func natural(p string) string {
	return numRe.ReplaceAllStringFunc(p, func(n string) string { return fmt.Sprintf("%08s", n) })
}

func readLabels(path string) (map[string]label, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	out := map[string]label{}
	for _, row := range rows {
		if len(row) < 2 {
			continue
		}
		l := strings.ToLower(strings.TrimSpace(row[1]))
		// Older files say good or bad. The scored format says publish,
		// small-edit or reject, then four scores from 1 to 5.
		switch l {
		case "good", "publish", "small-edit":
			l = "good"
		case "bad", "reject":
			l = "bad"
		default:
			continue
		}
		lb := label{good: l == "good"}
		rest := row[2:]
		if len(rest) >= 4 {
			var sc []int
			for _, c := range rest[:4] {
				n, err := strconv.Atoi(strings.TrimSpace(c))
				if err != nil || n < 1 || n > 5 {
					sc = nil
					break
				}
				sc = append(sc, n)
			}
			if sc != nil {
				lb.scores = sc
				rest = rest[4:]
			}
		}
		for _, c := range rest {
			if strings.Contains(c, "by-claude") {
				lb.byClaude = true
			}
		}
		out[strings.TrimSpace(row[0])] = lb
	}
	return out, nil
}

// class is what a verdict means for measuring.
const (
	clsBad    = "B"
	clsGood   = "G"
	clsReview = "R"
	clsError  = "E"
	clsMixed  = "U" // repeats disagree: unresolved
)

func classOf(verdict string) string {
	switch verdict {
	case eval.VerdictBad:
		return clsBad
	case eval.VerdictGood, eval.VerdictGoodSmallEdit:
		return clsGood
	case eval.VerdictNeedsReview:
		return clsReview
	}
	return clsError
}

// settle is the class of a series of repeats: the common class, or unresolved.
func settle(cs []string) string {
	if len(cs) == 0 {
		return clsError
	}
	for _, c := range cs {
		if c == clsError {
			return clsError
		}
	}
	for _, c := range cs[1:] {
		if c != cs[0] {
			return clsMixed
		}
	}
	return cs[0]
}

// result is everything measured for one script.
type result struct {
	s     sample
	code  eval.SpecReport
	byMdl map[string][]string // model -> class per repeat
	rep   map[string]eval.SpecReport
	notes map[string][]string
}

func wilsonUpper(k, n int) float64 {
	if n == 0 {
		return 1
	}
	const z = 1.96
	p, nn := float64(k)/float64(n), float64(n)
	centre := p + z*z/(2*nn)
	margin := z * math.Sqrt(p*(1-p)/nn+z*z/(4*nn*nn))
	return (centre + margin) / (1 + z*z/nn)
}

// tally counts one checker's settled classes against labels.
type tally struct {
	badRejected, badPassed, badOther    int
	goodRejected, goodPassed, goodOther int
}

func (t *tally) add(l label, cls string) {
	switch {
	case !l.good && cls == clsBad:
		t.badRejected++
	case !l.good && cls == clsGood:
		t.badPassed++
	case !l.good:
		t.badOther++
	case l.good && cls == clsBad:
		t.goodRejected++
	case l.good && cls == clsGood:
		t.goodPassed++
	default:
		t.goodOther++
	}
}

func (t tally) bad() int  { return t.badRejected + t.badPassed + t.badOther }
func (t tally) good() int { return t.goodRejected + t.goodPassed + t.goodOther }

func (t tally) line(who string) string {
	return fmt.Sprintf("| %s | %d of %d (upper %.0f%%) | %d | %d of %d (upper %.0f%%) | %d |",
		who, t.badPassed, t.bad(), wilsonUpper(t.badPassed, t.bad())*100, t.badOther,
		t.goodRejected, t.good(), wilsonUpper(t.goodRejected, t.good())*100, t.goodOther)
}

func summary(w io.Writer, results []result, labels map[string]label, models []string) {
	for _, group := range []struct {
		title string
		pick  func(label) bool
	}{
		{"owner labels", func(l label) bool { return !l.byClaude }},
		{"Claude labels (weaker)", func(l label) bool { return l.byClaude }},
	} {
		checkers := append([]string{"code checks"}, models...)
		if len(models) > 0 {
			checkers = append(checkers, "code + any model rejects")
		}
		tallies := map[string]*tally{}
		nl := 0
		for _, c := range checkers {
			tallies[c] = &tally{}
		}
		for _, r := range results {
			l, ok := labels[r.s.name]
			if !ok || !group.pick(l) {
				continue
			}
			nl++
			tallies["code checks"].add(l, classOf(r.code.Verdict))
			anyBad, anyMixed := false, false
			for _, m := range models {
				c := settle(r.byMdl[m])
				tallies[m].add(l, c)
				anyBad = anyBad || c == clsBad
				anyMixed = anyMixed || c == clsMixed
			}
			if len(models) > 0 {
				comb := classOf(r.code.Verdict)
				switch {
				case comb == clsBad || anyBad:
					comb = clsBad
				case anyMixed:
					comb = clsMixed
				}
				tallies["code + any model rejects"].add(l, comb)
			}
		}
		fmt.Fprintf(w, "\n%s (%d scripts)\n\n", group.title, nl)
		if nl == 0 {
			fmt.Fprintln(w, "none")
			continue
		}
		fmt.Fprintln(w, "| checker | bad let through | bad not decided | good rejected | good not decided |")
		fmt.Fprintln(w, "|---|---|---|---|---|")
		for _, c := range checkers {
			fmt.Fprintln(w, tallies[c].line(c))
		}
	}
}

func repeatability(w io.Writer, results []result, models []string, repeat int) {
	if repeat < 2 || len(models) == 0 {
		return
	}
	fmt.Fprintf(w, "\nRepeatability (%d runs per script)\n\n| model | scripts | verdicts that disagree between runs |\n|---|---|---|\n", repeat)
	for _, m := range models {
		n, mixed := 0, 0
		for _, r := range results {
			cs := r.byMdl[m]
			if len(cs) != repeat || settle(cs) == clsError {
				continue
			}
			n++
			if settle(cs) == clsMixed {
				mixed++
			}
		}
		fmt.Fprintf(w, "| %s | %d | %d of %d (upper %.0f%%) |\n", m, n, mixed, n, wilsonUpper(mixed, n)*100)
	}
}

func detail(w io.Writer, results []result, labels map[string]label, models []string) {
	fmt.Fprintln(w, "| script | label | code | "+strings.Join(models, " | ")+" | rejecting |")
	fmt.Fprintln(w, "|---|---|---|"+strings.Repeat("---|", len(models))+"---|")
	for _, r := range results {
		lb := "-"
		if l, ok := labels[r.s.name]; ok {
			lb = map[bool]string{true: "good", false: "bad"}[l.good]
			if l.byClaude {
				lb += " (claude)"
			}
		}
		cells := []string{r.s.name, lb, classOf(r.code.Verdict)}
		var why []string
		why = append(why, r.code.Rejecting...)
		for _, m := range models {
			cells = append(cells, strings.Join(r.byMdl[m], ""))
			for _, n := range r.notes[m] {
				why = append(why, m+": "+n)
			}
		}
		cells = append(cells, strings.Join(why, "; "))
		fmt.Fprintln(w, "| "+strings.Join(cells, " | ")+" |")
	}
	fmt.Fprintln(w, "\nB bad, G good, R needs review, U repeats disagree, E error.")
}

func newModels(mode judge.Mode) (map[string]judge.Checklist, []string, time.Duration) {
	key, spec := os.Getenv("GROQ_API_KEY"), os.Getenv("JUDGE_MODELS")
	pause := 10
	if v := os.Getenv("SCRIPT_PAUSE_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 600 {
			fmt.Fprintln(os.Stderr, "SCRIPT_PAUSE_SECONDS must be a number from 0 to 600")
			os.Exit(2)
		}
		pause = n
	}
	if key == "" || spec == "" {
		return nil, nil, 0
	}
	maxTokens := 4000
	if v := os.Getenv("JUDGE_MAX_TOKENS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 32000 {
			fmt.Fprintln(os.Stderr, "JUDGE_MAX_TOKENS must be a number from 1 to 32000")
			os.Exit(2)
		}
		maxTokens = n
	}
	out := map[string]judge.Checklist{}
	var names []string
	for _, item := range strings.Split(spec, ",") {
		model, effort, _ := strings.Cut(strings.TrimSpace(item), ":")
		g := llm.NewGroq(key, model)
		if err := g.SetReasoningEffort(effort); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", model, err)
			os.Exit(2)
		}
		p := &llm.Retrying{Inner: g, OnRetry: func(attempt int, wait time.Duration, _ error) {
			fmt.Fprintf(os.Stderr, "  rate limited or unavailable, waiting %s (retry %d of 3)\n", wait, attempt)
		}}
		name := model
		if effort != "" {
			name += ":" + effort
		}
		names = append(names, name)
		out[name] = judge.Checklist{Provider: p, MaxTokens: maxTokens, Mode: mode, ConfirmByPresence: os.Getenv("CONFIRM") == "presence"}
	}
	return out, names, time.Duration(pause) * time.Second
}

func main() {
	dirs := os.Args[1:]
	if len(dirs) == 0 {
		fmt.Fprintln(os.Stderr, "usage: specstats DIR...")
		os.Exit(2)
	}
	labelPath := os.Getenv("LABELS")
	if labelPath == "" {
		labelPath = "out/labels.csv"
	}
	labels, err := readLabels(labelPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	repeat := 1
	if v := os.Getenv("REPEAT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 10 {
			fmt.Fprintln(os.Stderr, "REPEAT must be a number from 1 to 10")
			os.Exit(2)
		}
		repeat = n
	}
	mode := judge.ModeOwnerReviews
	switch os.Getenv("MODE") {
	case "", "owner_reviews":
	case "autonomous":
		mode = judge.ModeAutonomous
	default:
		fmt.Fprintln(os.Stderr, "MODE must be owner_reviews or autonomous")
		os.Exit(2)
	}
	samples, err := load(dirs)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	checkers, models, pause := newModels(mode)

	var results []result
	first := true
	for _, s := range samples {
		if _, ok := labels[s.name]; !ok && os.Getenv("ALL") != "1" {
			continue
		}
		if !selected(s.name, os.Getenv("ONLY")) {
			continue
		}
		r := result{s: s, code: eval.CheckSpec(s.topic, s.title, s.body), byMdl: map[string][]string{}, rep: map[string]eval.SpecReport{}, notes: map[string][]string{}}
		sents := len(eval.SpecSentences(s.body))
		for _, m := range models {
			for i := 0; i < repeat; i++ {
				if !first {
					time.Sleep(pause)
				}
				first = false
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				cr, err := checkers[m].Review(ctx, s.topic, s.title, s.body)
				cancel()
				if err != nil {
					fmt.Fprintf(os.Stderr, "%s %s run %d: %v\n", s.name, m, i+1, err)
					r.byMdl[m] = append(r.byMdl[m], clsError)
					continue
				}
				if os.Getenv("VERBOSE") == "1" {
					fmt.Fprintf(os.Stderr, "%s %s run %d: %s\n", s.name, m, i+1, strings.Join(cr.Trace, "; "))
				}
				rep := eval.Decide(sents, append(append([]eval.Finding(nil), r.code.Findings...), cr.Findings...))
				r.byMdl[m] = append(r.byMdl[m], classOf(rep.Verdict))
				for _, id := range rep.Rejecting {
					r.notes[m] = append(r.notes[m], id)
				}
				for _, id := range cr.Dropped {
					r.notes[m] = append(r.notes[m], id+" dropped, no verbatim quote left")
				}
			}
		}
		results = append(results, r)
	}
	detail(os.Stdout, results, labels, models)
	summary(os.Stdout, results, labels, models)
	repeatability(os.Stdout, results, models, repeat)
	if len(models) == 0 {
		fmt.Fprintln(os.Stdout, "\nCode checks only (set GROQ_API_KEY and JUDGE_MODELS to add the checklist).")
	}
}
