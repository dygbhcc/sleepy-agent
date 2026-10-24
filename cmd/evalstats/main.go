// Command evalstats measures saved scripts with the quality metrics in
// internal/eval, which the QA gate does not look at.
//
// It reads the files that cmd/fixstats and cmd/scriptstats write (default
// out/fixstats, or the directories given as arguments), and for each script
// prints sentence shape, repetition and, for a script that a continuation
// extended, how much of the added text is new. It needs no network and no key.
//
// STYLE (default Earthside) says which imagery lines the prompt had given the
// model.
//
// With JUDGE=1 (and GROQ_API_KEY, GROQ_MODEL) the shadow judge scores each
// script too: calm, coherence, variety and narration from 1 to 5. JUDGE_MODELS
// (for example "openai/gpt-oss-120b:low,llama-3.3-70b-versatile") asks several
// models instead; a script then passes the panel only when all of them pass it,
// and the report compares the models with each other and with the labels. With LABELS=file (lines
// of "name,good" or "name,bad", name as in the table) it then reports how often
// the judge and the metrics agree with a human, which is what decides whether
// either may ever get a vote. If the LABELS file does not exist it is written
// as a template to fill in. JUDGE_PROMPT (v1 or v2, the wording of what coherence means), REASONING_EFFORT, JUDGE_MAX_TOKENS and
// SCRIPT_PAUSE_SECONDS work as in cmd/fixstats.
//
// It measures. It does not accept or reject anything, and its thresholds
// (eval.DefaultThresholds) are a first guess: read the numbers, not the flags.
package main

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
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

// sample is one saved script.
type sample struct {
	name     string
	arm      string
	topic    string
	title    string
	style    string // from the saved file; empty in files saved before Day 8
	body     string
	original string // set when a continuation extended an earlier script
}

var (
	finalMarker = "final script (what the run holds):"
	attemptRe   = regexp.MustCompile(`(?m)^(\d+)\. ([a-z ]+), (\d+) words,`)
	armRe       = regexp.MustCompile(`(?m)^arm: (\w+)`)
	topicRe     = regexp.MustCompile(`(?m)^topic: (.+)$`)
	titleRe     = regexp.MustCompile(`(?m)^title: (.+)$`)
	styleRe     = regexp.MustCompile(`(?m)^style: (.+)$`)
)

// parse reads one saved file. ok is false when it holds no script text.
func parse(name, text string) (sample, bool) {
	s := sample{name: name}
	if m := armRe.FindStringSubmatch(text); m != nil {
		s.arm = m[1]
	}
	if m := topicRe.FindStringSubmatch(text); m != nil {
		s.topic = strings.TrimSpace(m[1])
	}
	if m := titleRe.FindStringSubmatch(text); m != nil {
		s.title = strings.TrimSpace(m[1])
	}
	if m := styleRe.FindStringSubmatch(text); m != nil {
		s.style = strings.TrimSpace(m[1])
	}
	switch {
	case strings.Contains(text, finalMarker):
		s.body = strings.TrimSpace(text[strings.Index(text, finalMarker)+len(finalMarker):])
	case strings.HasPrefix(text, "title:"):
		if i := strings.Index(text, "\n\n"); i >= 0 {
			s.body = strings.TrimSpace(text[i:])
		}
	default:
		return s, false
	}
	if s.body == "" {
		return s, false
	}
	// A continuation appends to the script of the attempt before it.
	ms := attemptRe.FindAllStringSubmatch(text, -1)
	for i := 1; i < len(ms); i++ {
		if strings.TrimSpace(ms[i][2]) == "continue" {
			before, _ := strconv.Atoi(ms[i-1][3])
			if orig, ok := firstWords(s.body, before); ok {
				s.original = orig
			}
		}
	}
	return s, true
}

// firstWords returns the leading paragraphs of body that hold exactly n words.
func firstWords(body string, n int) (string, bool) {
	if n <= 0 {
		return "", false
	}
	var kept []string
	total := 0
	for _, p := range strings.Split(strings.TrimSpace(body), "\n\n") {
		if total >= n {
			break
		}
		kept = append(kept, p)
		total += len(strings.Fields(p))
	}
	if total != n {
		return "", false
	}
	return strings.Join(kept, "\n\n"), true
}

// styleOr is the style the script was written under: its own, or def for files
// saved before the style was recorded.
func (s sample) styleOr(def string) string {
	if s.style != "" {
		return s.style
	}
	return def
}

// concerns are everything the metrics flag about one script.
func concerns(s sample, t eval.Thresholds, def string) []string {
	c := t.Concerns(eval.Assess(s.body))
	if n := eval.Echoes(s.body, s.styleOr(def)); n > t.MaxEchoes {
		c = append(c, fmt.Sprintf("%d verbatim copies of the prompt's imagery lines (max %d)", n, t.MaxEchoes))
	}
	c = append(c, eval.StockImageryConcern(s.topic, s.body, s.styleOr(def))...)
	return append(c, eval.TopicConcerns(s.topic, s.title, s.body)...)
}

func load(dirs []string) ([]sample, error) {
	var out []sample
	for _, d := range dirs {
		files, err := filepath.Glob(filepath.Join(d, "*.md"))
		if err != nil {
			return nil, err
		}
		sort.Strings(files)
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

func report(w io.Writer, samples []sample, t eval.Thresholds, style string) {
	fmt.Fprintln(w, "| script | arm | style | words | sentences | under 8 words | over 20 words | repeated 4-grams | unique words | prompt lines copied | stock images touched | topic words in script | title keeps a topic word | concerns |")
	fmt.Fprintln(w, "|---|---|---|---|---|---|---|---|---|---|---|---|---|---|")
	flagged := 0
	for _, s := range samples {
		q := eval.Assess(s.body)
		echoes := eval.Echoes(s.body, s.styleOr(style))
		c := concerns(s, t, style)
		if len(c) > 0 {
			flagged++
		}
		fmt.Fprintf(w, "| %s | %s | %s | %d | %d | %.0f%% | %.0f%% | %.1f%% | %.2f | %d | %s | %s | %s | %s |\n", s.name, dash(s.arm), dash(s.style), q.Words, q.Sentences,
			q.ShortShare*100, q.LongShare*100, q.RepeatedGramShare*100, q.UniqueWordRatio, echoes, stockCell(s, style), topicCell(s), titleCell(s), orNone(c))
	}
	fmt.Fprintf(w, "\n%d of %d scripts have at least one concern.\n", flagged, len(samples))

	var rows []string
	add := 0
	for _, s := range samples {
		if s.original == "" {
			continue
		}
		a, ok := eval.Append(s.original, s.body)
		if !ok {
			continue
		}
		c := t.AdditionConcerns(a)
		if len(c) > 0 {
			add++
		}
		rows = append(rows, fmt.Sprintf("| %s | %d | %.0f%% | %.0f%% | %.0f%% | %s |", s.name, a.AddedWords,
			a.CopiedShare*100, a.NoveltyShare*100, a.ShortShare*100, orNone(c)))
	}
	if len(rows) > 0 {
		fmt.Fprintln(w, "\nText added by a continuation:")
		fmt.Fprintln(w, "\n| script | added words | added 4-grams already in the script | new content words | under 8 words | concerns |")
		fmt.Fprintln(w, "|---|---|---|---|---|---|")
		for _, r := range rows {
			fmt.Fprintln(w, r)
		}
		fmt.Fprintf(w, "\n%d of %d continuations have at least one concern.\n", add, len(rows))
	}
	fmt.Fprintln(w, "\nThresholds are a first guess (eval.DefaultThresholds). Compare the numbers, not the flags.")
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func orNone(c []string) string {
	if len(c) == 0 {
		return "none"
	}
	return strings.Join(c, "; ")
}

// reviewer is what the judge step needs; judge.Judge satisfies it.
type reviewer = judge.Reviewer

// verdicts holds the judge's answer per script name, or the error it gave.
type verdicts map[string]struct {
	v   judge.Verdict
	err error
}

func judgeAll(ctx context.Context, samples []sample, rv reviewer, pause time.Duration) verdicts {
	out := verdicts{}
	for i, s := range samples {
		if i > 0 {
			time.Sleep(pause)
		}
		rctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		v, err := rv.Review(rctx, s.topic, s.title, s.body)
		cancel()
		out[s.name] = struct {
			v   judge.Verdict
			err error
		}{v, err}
	}
	return out
}

func judgeReport(w io.Writer, samples []sample, vs verdicts, min int) {
	fmt.Fprintln(w, "\nShadow judge (scores 1 to 5):")
	fmt.Fprintln(w, "\n| script | calm | coherence | variety | narration | verdict | reason | evidence quoted |")
	fmt.Fprintln(w, "|---|---|---|---|---|---|---|---|")
	pass, rej, failed := 0, 0, 0
	for _, s := range samples {
		r := vs[s.name]
		switch {
		case r.err != nil:
			failed++
			fmt.Fprintf(w, "| %s | | | | | error | %s | |\n", s.name, oneLine(r.err.Error()))
		case r.v.Pass:
			pass++
			fmt.Fprintf(w, "| %s | %d | %d | %d | %d | pass | %s | %s |\n", s.name, r.v.Calm, r.v.Coherence, r.v.Variety, r.v.Narration, oneLine(r.v.Reason), oneLine(r.v.Evidence))
		default:
			rej++
			fmt.Fprintf(w, "| %s | %d | %d | %d | %d | would reject | %s | %s |\n", s.name, r.v.Calm, r.v.Coherence, r.v.Variety, r.v.Narration, oneLine(r.v.Reason), oneLine(r.v.Evidence))
		}
	}
	fmt.Fprintf(w, "\n%d pass, %d would reject, %d errors (a script passes when no score is under %d).\n", pass, rej, failed, min)
}

// readLabels reads "name,label" lines. Only good and bad count; an empty or
// other label means not labelled yet.
func readLabels(path string) (map[string]string, error) {
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
	out := map[string]string{}
	for _, row := range rows {
		if len(row) < 2 {
			continue
		}
		if l := strings.ToLower(strings.TrimSpace(row[1])); l == "good" || l == "bad" {
			out[strings.TrimSpace(row[0])] = l
		}
	}
	return out, nil
}

// writeLabelTemplate writes one line per script for a human to fill in.
func writeLabelTemplate(path string, samples []sample) error {
	var b strings.Builder
	for _, s := range samples {
		b.WriteString(s.name + ",\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// agreementReport compares a human's labels with the metrics and the judge.
// good means the human would let the script go out.
func agreementReport(w io.Writer, samples []sample, labels map[string]string, t eval.Thresholds, style string, vs verdicts) {
	type tally struct{ agree, total int }
	var metrics, jdg tally
	var missed []string // good or bad according to the human, and the tool said the opposite
	labelled := 0
	for _, s := range samples {
		label, ok := labels[s.name]
		if !ok {
			continue
		}
		labelled++
		humanGood := label == "good"
		flagged := len(concerns(s, t, style)) > 0
		metrics.total++
		if flagged != humanGood { // not flagged and good, or flagged and bad
			metrics.agree++
		} else {
			missed = append(missed, fmt.Sprintf("%s: human %s, metrics %s", s.name, label, verdictWord(!flagged)))
		}
		if r, ok := vs[s.name]; ok && r.err == nil {
			jdg.total++
			if r.v.Pass == humanGood {
				jdg.agree++
			} else {
				missed = append(missed, fmt.Sprintf("%s: human %s, judge %s", s.name, label, verdictWord(r.v.Pass)))
			}
		}
	}
	fmt.Fprintf(w, "\nAgreement with the human labels (%d of %d scripts labelled):\n\n", labelled, len(samples))
	fmt.Fprintln(w, "| checker | agrees | of |")
	fmt.Fprintln(w, "|---|---|---|")
	fmt.Fprintf(w, "| metrics (concerns or echoes means bad) | %d | %d |\n", metrics.agree, metrics.total)
	if len(vs) > 0 {
		fmt.Fprintf(w, "| shadow judge | %d | %d |\n", jdg.agree, jdg.total)
	}
	if len(missed) > 0 {
		fmt.Fprintln(w, "\nDisagreements:")
		for _, m := range missed {
			fmt.Fprintln(w, "-", m)
		}
	}
	fmt.Fprintln(w, "\nA checker earns a vote only with enough labelled scripts and agreement well above what guessing gives. Counts this small say little.")
}

func verdictWord(ok bool) string {
	if ok {
		return "good"
	}
	return "bad"
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 140 {
		s = s[:140] + "..."
	}
	return s
}

func main() {
	dirs := os.Args[1:]
	if len(dirs) == 0 {
		dirs = []string{filepath.Join("out", "fixstats")}
	}
	samples, err := load(dirs)
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed:", err)
		os.Exit(1)
	}
	if len(samples) == 0 {
		fmt.Fprintln(os.Stderr, "no saved scripts found in", strings.Join(dirs, ", "))
		os.Exit(1)
	}
	style := os.Getenv("STYLE")
	if style == "" {
		style = "Earthside"
	}
	report(os.Stdout, samples, eval.DefaultThresholds, style)

	var vs verdicts
	var byModel []named
	if os.Getenv("JUDGE") != "" {
		members, pause := newPanel()
		for _, m := range members {
			fmt.Printf("\njudge model: %s\n", m.Name)
			byModel = append(byModel, named{m.Name, judgeAll(context.Background(), samples, m.Reviewer, pause)})
		}
		if len(byModel) == 1 {
			vs = byModel[0].vs
			judgeReport(os.Stdout, samples, vs, judge.DefaultMin)
		} else {
			for _, b := range byModel {
				fmt.Printf("\n%s:\n", b.name)
				judgeReport(os.Stdout, samples, b.vs, judge.DefaultMin)
			}
			vs = combineVerdicts(samples, byModel)
		}
	}
	if path := os.Getenv("LABELS"); path != "" {
		labels, err := readLabels(path)
		switch {
		case errors.Is(err, os.ErrNotExist):
			if err := writeLabelTemplate(path, samples); err != nil {
				fmt.Fprintln(os.Stderr, "failed:", err)
				os.Exit(1)
			}
			fmt.Printf("\nWrote a label template to %s: put good or bad after each comma and run again.\n", path)
		case err != nil:
			fmt.Fprintln(os.Stderr, "failed:", err)
			os.Exit(1)
		default:
			agreementReport(os.Stdout, samples, labels, eval.DefaultThresholds, style, vs)
			if len(byModel) > 0 {
				panelReport(os.Stdout, samples, labels, byModel, vs)
			}
		}
	}
}

// newPanel builds the judges from the environment. A key is required.
// JUDGE_MODELS is a comma separated list of "model" or "model:effort", for
// example "openai/gpt-oss-120b:low,llama-3.3-70b-versatile". Without it the
// single judge is GROQ_MODEL with REASONING_EFFORT, as before. A model that is
// not a reasoning model must be given no effort: Groq may reject the setting.
func newPanel() ([]judge.Member, time.Duration) {
	key := os.Getenv("GROQ_API_KEY")
	spec := os.Getenv("JUDGE_MODELS")
	if spec == "" {
		model := os.Getenv("GROQ_MODEL")
		if model != "" && os.Getenv("REASONING_EFFORT") != "" {
			model += ":" + os.Getenv("REASONING_EFFORT")
		}
		spec = model
	}
	if key == "" || spec == "" {
		fmt.Fprintln(os.Stderr, "JUDGE=1 needs GROQ_API_KEY and JUDGE_MODELS (or GROQ_MODEL)")
		os.Exit(2)
	}
	maxTokens := 2000
	if v := os.Getenv("JUDGE_MAX_TOKENS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 32000 {
			fmt.Fprintln(os.Stderr, "JUDGE_MAX_TOKENS must be a number from 1 to 32000")
			os.Exit(2)
		}
		maxTokens = n
	}
	rubric := os.Getenv("JUDGE_PROMPT")
	if _, ok := judge.PromptFor(rubric); !ok {
		fmt.Fprintln(os.Stderr, "JUDGE_PROMPT must be v1 or v2")
		os.Exit(2)
	}
	var members []judge.Member
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
		name := "groq:" + model
		if effort != "" {
			name += " [reasoning " + effort + "]"
		}
		if rubric == judge.RubricV2 {
			name += " [prompt v2]"
		}
		members = append(members, judge.Member{Name: name, Reviewer: judge.Judge{Provider: p, MaxTokens: maxTokens, Rubric: rubric}})
	}
	pause := 15
	if v := os.Getenv("SCRIPT_PAUSE_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 600 {
			fmt.Fprintln(os.Stderr, "SCRIPT_PAUSE_SECONDS must be a number from 0 to 600")
			os.Exit(2)
		}
		pause = n
	}
	return members, time.Duration(pause) * time.Second
}

// topicCell is "covered/of (mentions)" for the script, "-" when the file has
// no topic line.
func topicCell(s sample) string {
	if len(eval.TopicWords(s.topic)) == 0 {
		return "-"
	}
	cov, of, n := eval.TopicCoverage(s.topic, s.body)
	return fmt.Sprintf("%d/%d (%d)", cov, of, n)
}

func titleCell(s sample) string {
	if len(eval.TopicWords(s.topic)) == 0 || strings.TrimSpace(s.title) == "" {
		return "-"
	}
	if cov, _, _ := eval.TopicCoverage(s.topic, s.title); cov > 0 {
		return "yes"
	}
	return "no"
}

// stockCell is "touched/countable" stock images for the script.
func stockCell(s sample, def string) string {
	used, of := eval.StockImageryUsed(s.topic, s.body, s.styleOr(def))
	if of == 0 {
		return "-"
	}
	return fmt.Sprintf("%d/%d", used, of)
}
