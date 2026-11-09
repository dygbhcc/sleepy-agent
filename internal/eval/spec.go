package eval

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

// This file is the code half of docs/quality-spec.md: the defects that can be
// counted. Every finding carries a number and a quote, and says whether it
// reaches its reject threshold or is only a warning. The thresholds are the
// spec's, and they were chosen by looking at labelled scripts, so they are
// provisional until a fresh batch confirms them (spec section 9).
//
// Like the rest of this package it only measures: nothing here rejects a run.

// Verdicts the spec can give.
const (
	VerdictGood           = "good"
	VerdictGoodSmallEdit  = "good_after_small_edit"
	VerdictBad            = "bad"
	VerdictNeedsReview    = "needs_review"
	smallEditMinSentences = 5
	smallEditShare        = 0.10
)

// Finding is one defect check. Sentences are indexes into the script's
// sentences, so findings from several checks can be added up.
type Finding struct {
	ID     string  `json:"id"`
	Count  int     `json:"count"`
	Share  float64 `json:"share"`
	Quote  string  `json:"quote"`
	Reject bool    `json:"reject"`
	Warn   bool    `json:"warn"`
	// Unsure is a judge that could not decide. Under the autonomous mode it
	// sends the script to review (spec section 6a).
	Unsure bool `json:"unsure,omitempty"`
	// Advisory is a warning that is reported and quoted but never counts
	// toward the combined rule: its threshold is not confirmed by the owner.
	Advisory  bool  `json:"advisory,omitempty"`
	Sentences []int `json:"sentences,omitempty"`
}

// SpecReport is the code checks' result for one script.
type SpecReport struct {
	Sentences int       `json:"sentences"`
	Findings  []Finding `json:"findings"`
	Verdict   string    `json:"verdict"`
	Rejecting []string  `json:"rejecting"`
	Warnings  []string  `json:"warnings"`
	FixQuotes []string  `json:"fix_quotes"`
}

// SmallEditLimit is how many sentences a small edit may touch: 5 or 10 percent
// of the script, whichever is larger.
func SmallEditLimit(sentences int) int {
	l := int(math.Ceil(float64(sentences) * smallEditShare))
	if l < smallEditMinSentences {
		l = smallEditMinSentences
	}
	return l
}

// SpecSentences splits at newlines and at . ! ? that end a sentence. The text
// keeps its closing punctuation.
func SpecSentences(body string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r", ""), "\n") {
		line = strings.TrimSpace(line)
		start := 0
		runes := []rune(line)
		for i, r := range runes {
			if (r == '.' || r == '!' || r == '?') && (i+1 == len(runes) || runes[i+1] == ' ') {
				if s := strings.TrimSpace(string(runes[start : i+1])); s != "" {
					out = append(out, s)
				}
				start = i + 1
			}
		}
		if s := strings.TrimSpace(string(runes[start:])); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// specParagraphs are blocks separated by a blank line. A script with no blank
// lines is read as groups of 4 sentences.
// SpecParagraphs are blocks separated by a blank line. A script with no blank
// lines is read as groups of 4 sentences.
func SpecParagraphs(body string) []string {
	return specParagraphs(body, SpecSentences(body))
}

func specParagraphs(body string, sents []string) []string {
	var out []string
	for _, p := range strings.Split(strings.ReplaceAll(body, "\r", ""), "\n\n") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) > 1 {
		return out
	}
	out = out[:0]
	for i := 0; i < len(sents); i += 4 {
		end := i + 4
		if end > len(sents) {
			end = len(sents)
		}
		out = append(out, strings.Join(sents[i:end], " "))
	}
	return out
}

func trimEnd(s string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(s), ".!?\"'”’ "))
}

var fillerTails = []string{"softly", "tonight", "today", "now", "here", "in silence", "in quietness",
	"of peace", "for souls", "onward forward", "ever gentler", "for night"}

func hasFillerTail(s string) bool {
	t := trimEnd(s)
	for _, p := range fillerTails {
		if t == p || strings.HasSuffix(t, " "+p) {
			return true
		}
	}
	return false
}

var danglingEnds = set(strings.Fields("the a an of in by and to for with"))

func lastWord(s string) string {
	t := tokens(trimEnd(s))
	if len(t) == 0 {
		return ""
	}
	return t[len(t)-1]
}

// Thresholds of the spec's count based checks. A count at or above Reject is a
// reject; fewer than that, but at least one, is a warning.
const (
	h1WarnShare = 0.10
	// h1RejectShare is provisional: it sits between the highest share seen in a
	// script the owner called good (38 percent) and an owner-bad script with
	// filler on every sentence (100 percent). Nothing was measured between
	// 42 and 100 percent. It is validated on strict-d, set before labelling.
	h1RejectShare   = 0.60
	h2RejectCount   = 6
	h3RejectCount   = 6
	h3OpeningShare  = 0.40
	h3OverlapShare  = 0.60
	h3MinParagraphs = 5
)

func newFinding(id string, idx []int, sents []string, rejectAt int) Finding {
	f := Finding{ID: id, Count: len(idx), Sentences: idx}
	if len(sents) > 0 {
		f.Share = float64(len(idx)) / float64(len(sents))
	}
	if len(idx) > 0 {
		f.Quote = sents[idx[0]]
	}
	f.Reject = rejectAt > 0 && len(idx) >= rejectAt
	f.Warn = len(idx) > 0 && !f.Reject
	return f
}

// topicKeys are the nouns a script must name: the word before "at", "on" or
// "in", the word after "of", and one word on each side of "and".
func topicKeys(topic string) []string {
	var keys []string
	for _, part := range regexp.MustCompile(`(?i)\s+and\s+`).Split(topic, -1) {
		w := strings.Fields(strings.ToLower(part))
		cut := -1
		of := false
		for i, x := range w {
			if x == "of" || x == "at" || x == "on" || x == "in" {
				cut, of = i, x == "of"
				break
			}
		}
		var side []string
		switch {
		case cut < 0:
			side = w
		case of:
			side = w[cut+1:]
		default:
			side = w[:cut]
		}
		side = TopicWords(strings.Join(side, " "))
		if len(side) > 0 {
			keys = append(keys, side[len(side)-1])
		}
	}
	return keys
}

// CheckSpec runs the code checks of the spec on one script: H1, H2, H3, H4 and
// H8, and the title warning.
func CheckSpec(topic, title, body string) SpecReport {
	sents := SpecSentences(body)
	var fs []Finding

	// H1 filler tail.
	var h1 []int
	for i, s := range sents {
		if hasFillerTail(s) {
			h1 = append(h1, i)
		}
	}
	f := newFinding("H1", h1, sents, 0)
	// v3: at 25 percent H1 flagged 4 of 8 scripts the owner called good
	// (strict-c), so the reject threshold is now 60 percent (provisional).
	// Below it H1 is an advisory warning that never counts toward the
	// combined rule.
	f.Reject = f.Share >= h1RejectShare
	f.Warn = len(h1) > 0 && !f.Reject && f.Share >= h1WarnShare
	f.Advisory = true
	if !f.Reject && !f.Warn {
		f.Sentences = nil
	}
	fs = append(fs, f)

	// H11 unsettling words (candidate): advisory until the owner confirms the
	// list and a threshold.
	var h11 []int
	for i, s := range sents {
		if unsettlingRe.MatchString(s) {
			h11 = append(h11, i)
		}
	}
	if len(h11) > 0 {
		f11 := newFinding("H11", h11, sents, 0)
		f11.Reject = false
		f11.Warn = true
		f11.Advisory = true
		fs = append(fs, f11)
	}

	// H2 dangling sentence.
	var h2 []int
	for i, s := range sents {
		if danglingEnds[lastWord(s)] {
			h2 = append(h2, i)
		}
	}
	fs = append(fs, newFinding("H2", h2, sents, h2RejectCount))

	// H3a exact duplicate sentences: every occurrence after the first.
	seen := map[string]bool{}
	var h3 []int
	for i, s := range sents {
		k := strings.Join(tokens(s), " ")
		if k == "" {
			continue
		}
		if seen[k] {
			h3 = append(h3, i)
		}
		seen[k] = true
	}
	fs = append(fs, newFinding("H3a", h3, sents, h3RejectCount))

	paras := specParagraphs(body, sents)
	if len(paras) >= h3MinParagraphs {
		// H3b the same four opening words in 40 percent of paragraphs.
		open := map[string]int{}
		for _, p := range paras {
			if t := tokens(p); len(t) >= 4 {
				open[strings.Join(t[:4], " ")]++
			}
		}
		for k, n := range open {
			if float64(n)/float64(len(paras)) >= h3OpeningShare {
				fs = append(fs, Finding{ID: "H3b", Count: n, Share: float64(n) / float64(len(paras)), Quote: k, Reject: true})
				break
			}
		}
		// H3d two paragraphs that share 60 percent of their content words.
		sets := make([]map[string]bool, len(paras))
		for i, p := range paras {
			sets[i] = contentWords(tokens(p))
		}
	pairs:
		for i := 0; i < len(sets); i++ {
			for j := i + 1; j < len(sets); j++ {
				small := len(sets[i])
				if len(sets[j]) < small {
					small = len(sets[j])
				}
				if small == 0 {
					continue
				}
				common := 0
				for w := range sets[i] {
					if sets[j][w] {
						common++
					}
				}
				if share := float64(common) / float64(small); share >= h3OverlapShare {
					fs = append(fs, Finding{ID: "H3d", Count: 1, Share: share, Quote: clipQuote(paras[j]), Reject: true})
					break pairs
				}
			}
		}
	}
	// H3c a run of 3 or 4 sentences that appears twice.
	norm := make([]string, len(sents))
	for i, s := range sents {
		norm[i] = strings.Join(tokens(s), " ")
	}
cycles:
	for _, k := range []int{3, 4} {
		first := map[string]int{}
		for i := 0; i+k <= len(norm); i++ {
			key := strings.Join(norm[i:i+k], "|")
			if j, ok := first[key]; ok && i >= j+k {
				fs = append(fs, Finding{ID: "H3c", Count: 1, Quote: sents[i], Reject: true})
				break cycles
			}
			if _, ok := first[key]; !ok {
				first[key] = i
			}
		}
	}

	// H4 candidate: a key noun of the topic appears fewer than 2 times.
	for _, key := range topicKeys(topic) {
		n := 0
		for _, t := range tokens(body) {
			if stem(possessive(t)) == key {
				n++
			}
		}
		// Counting words cannot reject: a library script can name shelves,
		// books and pages and never say "library" in the body (two scripts the
		// owner or Claude called good do exactly that). The finding is a
		// candidate for the judge, which decides whether the subject is shown.
		if n < 2 {
			fs = append(fs, Finding{ID: "H4", Count: n, Quote: fmt.Sprintf("%q appears %d times", key, n), Warn: true})
		}
	}

	// H8 the script talks about itself.
	var h8 []int
	for i, s := range sents {
		if metaRe.MatchString(s) || closingRe.MatchString(s) {
			h8 = append(h8, i)
		}
	}
	f8 := newFinding("H8", h8, sents, 1)
	fs = append(fs, f8)

	// Title: a warning, never a reason to reject.
	if strings.TrimSpace(title) != "" && len(TopicWords(topic)) > 0 {
		if cov, _, _ := TopicCoverage(topic, title); cov == 0 {
			fs = append(fs, Finding{ID: "T1", Count: 1, Quote: title, Warn: true})
		}
	}

	return decide(len(sents), keepFindings(fs))
}

// possessive drops a trailing 's, so "lighthouse's" counts as "lighthouse".
func possessive(w string) string {
	for _, suf := range []string{"'s", "\u2019s"} {
		w = strings.TrimSuffix(w, suf)
	}
	return w
}

// unsettlingRe lists words that do not help someone relax. "blade" is left out
// because it usually means a blade of grass. The list is not confirmed.
var unsettlingRe = regexp.MustCompile(`(?i)\b(insects?|beetles?|bugs?|spiders?|stings?|stinging|knife|knives)\b`)

var closingRe = regexp.MustCompile(`(?i)\bclosing stillness\b`)

func clipQuote(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 140 {
		return string(r[:140]) + "..."
	}
	return s
}

func keepFindings(fs []Finding) []Finding {
	out := fs[:0]
	for _, f := range fs {
		if f.Reject || f.Warn || f.Unsure {
			out = append(out, f)
		}
	}
	return out
}

// Decide applies the spec's decision rule to findings that may come from the
// code checks and from the judge's checklist. sentences is the script's
// sentence count. Findings with neither Reject nor Warn are ignored.
func Decide(sentences int, findings []Finding) SpecReport {
	return decide(sentences, keepFindings(append([]Finding(nil), findings...)))
}

func decide(n int, fs []Finding) SpecReport {
	r := SpecReport{Sentences: n, Findings: fs, Rejecting: []string{}, Warnings: []string{}, FixQuotes: []string{}}
	touched := map[int]bool{}
	for _, f := range fs {
		switch {
		case f.Unsure && !f.Reject && !f.Warn:
			// reported through the verdict only
		case f.Reject:
			r.Rejecting = append(r.Rejecting, f.ID)
		case f.Warn:
			r.Warnings = append(r.Warnings, f.ID)
			if !f.Advisory {
				for _, i := range f.Sentences {
					touched[i] = true
				}
			}
			if f.Quote != "" {
				r.FixQuotes = append(r.FixQuotes, f.Quote)
			}
		}
	}
	unsure := false
	for _, f := range fs {
		unsure = unsure || f.Unsure
	}
	switch {
	case len(r.Rejecting) > 0:
		r.Verdict = VerdictBad
	case len(touched) > SmallEditLimit(n):
		// Many small slips of different kinds add up to more than a small edit.
		r.Verdict = VerdictBad
		r.Rejecting = append(r.Rejecting, "combined")
	case unsure:
		r.Verdict = VerdictNeedsReview
	case len(r.Warnings) > 0:
		r.Verdict = VerdictGoodSmallEdit
	default:
		r.Verdict = VerdictGood
	}
	return r
}
