package script

import (
	"regexp"
	"sort"
	"strings"
	"unicode"

	"sleepy-agent/internal/policy"
)

// Violation names. They follow the rules the prompt gives the model, so a
// report says how often the model broke the rules it was told.
const (
	TooShort         = "too_short"
	TooLong          = "too_long"
	BannedWord       = "banned_word"
	Exclamation      = "exclamation_mark"
	Decoration       = "ellipsis_or_dash"
	Markdown         = "markdown_or_heading"
	AllCaps          = "all_caps_word"
	SentenceLength   = "sentence_not_8_to_20_words"
	ParagraphShape   = "paragraph_not_4_sentences"
	RepeatedSentence = "repeated_sentence"
)

// Report is what Measure found. It judges nothing: Violations lists the rules
// that were broken, and a later day decides what to do about them (Day 6 turns
// them into gates).
type Report struct {
	Words      int
	Paragraphs int
	Sentences  int

	SentencesOutOfRange   int      // sentences with fewer than 8 or more than 20 words
	ParagraphsOffShape    int      // paragraphs that do not have exactly 4 sentences
	BannedFound           []string // sorted, each phrase once
	Exclamations          int
	DecorativePunct       int // "...", the ellipsis character, em and en dashes
	MarkdownLines         int
	AllCapsWords          int
	RepeatedSentenceCount int // sentences that occur more than once, counted beyond the first

	Violations []string // rule names, in a fixed order
}

// Clean reports whether no rule was broken.
func (r Report) Clean() bool { return len(r.Violations) == 0 }

var bannedRe = func() *regexp.Regexp {
	parts := make([]string, len(BannedPhrases))
	for i, p := range BannedPhrases {
		parts[i] = regexp.QuoteMeta(p)
	}
	return regexp.MustCompile(`(?i)\b(` + strings.Join(parts, "|") + `)\b`)
}()

// BannedHit is one banned phrase and how often it occurs in a text.
type BannedHit struct {
	Phrase string
	Count  int
}

// BannedCounts finds the banned phrases in text: whole words only, case
// insensitive, listed in the order of BannedPhrases. Measure and the QA gate
// both use it, so they can never disagree about what counts as a banned word.
func BannedCounts(text string) []BannedHit {
	counts := map[string]int{}
	for _, m := range bannedRe.FindAllString(text, -1) {
		counts[strings.ToLower(m)]++
	}
	var out []BannedHit
	for _, p := range BannedPhrases {
		if n := counts[strings.ToLower(p)]; n > 0 {
			out = append(out, BannedHit{Phrase: p, Count: n})
		}
	}
	return out
}

var markdownLineRe = regexp.MustCompile(`^\s*(#{1,6}\s|[-*+]\s|\d+[.)]\s|>\s)`)

// Measure counts how a script follows the rules in the prompt, against the
// word limits in p. Banned words match whole words only, like Sleepy.
func Measure(text string, p policy.Policy) Report {
	var r Report
	r.Words = len(strings.Fields(text))

	paragraphs := splitParagraphs(text)
	r.Paragraphs = len(paragraphs)

	seen := map[string]int{}
	for _, para := range paragraphs {
		sents := sentences(para)
		if len(sents) != 4 {
			r.ParagraphsOffShape++
		}
		for _, s := range sents {
			r.Sentences++
			if n := len(strings.Fields(s)); n < 8 || n > 20 {
				r.SentencesOutOfRange++
			}
			seen[strings.ToLower(strings.TrimRight(strings.TrimSpace(s), ".!?"))]++
		}
	}
	for _, n := range seen {
		if n > 1 {
			r.RepeatedSentenceCount += n - 1
		}
	}

	for _, h := range BannedCounts(text) {
		r.BannedFound = append(r.BannedFound, h.Phrase)
	}
	sort.Strings(r.BannedFound)

	r.Exclamations = strings.Count(text, "!")
	r.DecorativePunct = strings.Count(text, "...") + strings.Count(text, "…") +
		strings.Count(text, "—") + strings.Count(text, "–")
	for _, line := range strings.Split(text, "\n") {
		if markdownLineRe.MatchString(line) || strings.Contains(line, "**") || strings.Contains(line, "```") {
			r.MarkdownLines++
		}
	}
	for _, w := range strings.Fields(text) {
		if isAllCaps(w) {
			r.AllCapsWords++
		}
	}

	add := func(cond bool, name string) {
		if cond {
			r.Violations = append(r.Violations, name)
		}
	}
	add(r.Words < p.MinWords, TooShort)
	add(r.Words > p.MaxWords, TooLong)
	add(len(r.BannedFound) > 0, BannedWord)
	add(r.Exclamations > 0, Exclamation)
	add(r.DecorativePunct > 0, Decoration)
	add(r.MarkdownLines > 0, Markdown)
	add(r.AllCapsWords > 0, AllCaps)
	add(r.SentencesOutOfRange > 0, SentenceLength)
	add(r.ParagraphsOffShape > 0, ParagraphShape)
	add(r.RepeatedSentenceCount > 0, RepeatedSentence)
	return r
}

// sentences splits a paragraph at ., ! and ? followed by a space or the end.
func sentences(para string) []string {
	var out []string
	runes := []rune(para)
	start := 0
	for i, r := range runes {
		if r != '.' && r != '!' && r != '?' {
			continue
		}
		if i+1 < len(runes) && !unicode.IsSpace(runes[i+1]) {
			continue // a decimal point or an abbreviation inside a word
		}
		if s := strings.TrimSpace(string(runes[start : i+1])); s != "" {
			out = append(out, s)
		}
		start = i + 1
	}
	if tail := strings.TrimSpace(string(runes[start:])); tail != "" {
		out = append(out, tail) // text with no closing punctuation is still a sentence
	}
	return out
}

func isAllCaps(w string) bool {
	letters := 0
	for _, r := range w {
		if unicode.IsLetter(r) {
			letters++
			if !unicode.IsUpper(r) {
				return false
			}
		}
	}
	return letters >= 2
}
