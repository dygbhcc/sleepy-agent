// Package eval measures the quality of a script beyond what the QA gate checks.
//
// The gate (internal/qa) decides accept or reject on word count, banned
// phrases, sleep safety and the title. It does not look at how the sentences
// are built, whether the text repeats itself, or whether text a fix appended
// says anything new. Day 6 found a script the gate accepted with 32 of 84
// sentences under 8 words, and Day 7 found continuations that raise the word
// count by construction. This package measures those things.
//
// Nothing here changes a run. The numbers are for measurement first: the
// thresholds in DefaultThresholds are a starting point set from a handful of
// real scripts, not Sleepy's production values, and they say so.
package eval

import (
	"fmt"
	"strings"
	"unicode"

	"sleepy-agent/internal/script"
)

// Quality is what Assess found in a script body.
type Quality struct {
	Words     int
	Sentences int

	AvgSentenceWords float64
	// ShortShare and LongShare are the share of sentences under 8 and over 20
	// words, the range the prompt asks for.
	ShortShare float64
	LongShare  float64
	// RepeatedGramShare is the share of word 4-grams that occur more than once.
	// One repeated sentence or a looped phrase raises it.
	RepeatedGramShare float64
	// UniqueWordRatio is distinct words over all words, case folded.
	UniqueWordRatio float64
}

// Assess measures a script body. An empty body returns the zero value.
func Assess(body string) Quality {
	var q Quality
	words := tokens(body)
	q.Words = len(words)
	if q.Words == 0 {
		return q
	}
	sents := script.Sentences(body)
	q.Sentences = len(sents)
	var short, long, total int
	for _, s := range sents {
		n := len(strings.Fields(s))
		total += n
		switch {
		case n < 8:
			short++
		case n > 20:
			long++
		}
	}
	if q.Sentences > 0 {
		q.AvgSentenceWords = float64(total) / float64(q.Sentences)
		q.ShortShare = float64(short) / float64(q.Sentences)
		q.LongShare = float64(long) / float64(q.Sentences)
	}
	q.RepeatedGramShare = repeatedShare(grams(words, 4))
	q.UniqueWordRatio = float64(len(set(words))) / float64(len(words))
	return q
}

// Echoes counts how often the script repeats the prompt's own imagery lines
// word for word, ignoring case and the closing period. The prompt hands the
// model ten lines per style to rotate through, and a model that pastes them
// into most paragraphs produces text that reads as a list of unrelated images.
// An unknown style counts nothing.
func Echoes(body, style string) int {
	text := strings.ToLower(body)
	n := 0
	for _, line := range script.ImageryLines(style) {
		phrase := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(line), "."))
		if phrase != "" {
			n += strings.Count(text, phrase)
		}
	}
	return n
}

// Addition is how a block of appended text relates to the text before it.
type Addition struct {
	AddedWords int
	// CopiedShare is the share of the added 4-grams that already occur in the
	// original: text copied or paraphrased very closely.
	CopiedShare float64
	// NoveltyShare is the share of the added text's distinct content words
	// (words of 4 or more letters) that the original does not use. Low means
	// the addition recycles the images and words the script already had.
	NoveltyShare float64
	ShortShare   float64
}

// Append compares full against original, where full is original followed by
// appended text, as a continuation produces. It reports false when full does
// not start with original or nothing was added.
func Append(original, full string) (Addition, bool) {
	original, full = strings.TrimSpace(original), strings.TrimSpace(full)
	if original == "" || !strings.HasPrefix(full, original) {
		return Addition{}, false
	}
	added := strings.TrimSpace(strings.TrimPrefix(full, original))
	addedWords := tokens(added)
	if len(addedWords) == 0 {
		return Addition{}, false
	}
	a := Addition{AddedWords: len(addedWords), ShortShare: Assess(added).ShortShare}

	before := set(grams(tokens(original), 4))
	ag := grams(addedWords, 4)
	if len(ag) > 0 {
		copied := 0
		for _, g := range ag {
			if before[g] {
				copied++
			}
		}
		a.CopiedShare = float64(copied) / float64(len(ag))
	}

	known := contentWords(tokens(original))
	fresh := contentWords(addedWords)
	if len(fresh) > 0 {
		novel := 0
		for w := range fresh {
			if !known[w] {
				novel++
			}
		}
		a.NoveltyShare = float64(novel) / float64(len(fresh))
	}
	return a, true
}

// Thresholds turn measurements into concerns. They are a first guess from a
// small sample of real scripts, to be revisited when there is more data.
type Thresholds struct {
	MaxShortShare        float64
	MaxLongShare         float64
	MaxRepeatedGramShare float64
	MinUniqueWordRatio   float64
	MaxCopiedShare       float64
	MinNoveltyShare      float64
	// MaxEchoes is how many verbatim copies of the prompt's imagery lines a
	// script may contain. The prompt has ten lines, so ten is one use each.
	MaxEchoes int
}

// DefaultThresholds are the starting values. See the package comment.
var DefaultThresholds = Thresholds{
	MaxShortShare:        0.10,
	MaxLongShare:         0.10,
	MaxRepeatedGramShare: 0.05,
	MinUniqueWordRatio:   0.35,
	MaxCopiedShare:       0.10,
	MinNoveltyShare:      0.30,
	MaxEchoes:            10,
}

// Concerns lists what is wrong with a script, empty if nothing is.
func (t Thresholds) Concerns(q Quality) []string {
	var c []string
	if q.ShortShare > t.MaxShortShare {
		c = append(c, fmt.Sprintf("%.0f%% of sentences are under 8 words (max %.0f%%)", q.ShortShare*100, t.MaxShortShare*100))
	}
	if q.LongShare > t.MaxLongShare {
		c = append(c, fmt.Sprintf("%.0f%% of sentences are over 20 words (max %.0f%%)", q.LongShare*100, t.MaxLongShare*100))
	}
	if q.RepeatedGramShare > t.MaxRepeatedGramShare {
		c = append(c, fmt.Sprintf("%.0f%% of 4-word phrases repeat (max %.0f%%)", q.RepeatedGramShare*100, t.MaxRepeatedGramShare*100))
	}
	if q.Words >= 50 && q.UniqueWordRatio < t.MinUniqueWordRatio {
		c = append(c, fmt.Sprintf("unique word ratio %.2f (min %.2f)", q.UniqueWordRatio, t.MinUniqueWordRatio))
	}
	return c
}

// AdditionConcerns lists what is wrong with appended text, empty if nothing is.
func (t Thresholds) AdditionConcerns(a Addition) []string {
	var c []string
	if a.CopiedShare > t.MaxCopiedShare {
		c = append(c, fmt.Sprintf("%.0f%% of the added 4-word phrases are already in the script (max %.0f%%)", a.CopiedShare*100, t.MaxCopiedShare*100))
	}
	if a.NoveltyShare < t.MinNoveltyShare {
		c = append(c, fmt.Sprintf("only %.0f%% of the added content words are new (min %.0f%%)", a.NoveltyShare*100, t.MinNoveltyShare*100))
	}
	if a.ShortShare > t.MaxShortShare {
		c = append(c, fmt.Sprintf("%.0f%% of the added sentences are under 8 words (max %.0f%%)", a.ShortShare*100, t.MaxShortShare*100))
	}
	return c
}

// tokens lowercases and strips punctuation from the edges of each word.
func tokens(text string) []string {
	f := strings.Fields(strings.ToLower(text))
	out := f[:0]
	for _, w := range f {
		w = strings.TrimFunc(w, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
		if w != "" {
			out = append(out, w)
		}
	}
	return out
}

func grams(words []string, n int) []string {
	if len(words) < n {
		return nil
	}
	out := make([]string, 0, len(words)-n+1)
	for i := 0; i+n <= len(words); i++ {
		out = append(out, strings.Join(words[i:i+n], " "))
	}
	return out
}

func set(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}

func repeatedShare(gs []string) float64 {
	if len(gs) == 0 {
		return 0
	}
	count := map[string]int{}
	for _, g := range gs {
		count[g]++
	}
	rep := 0
	for _, g := range gs {
		if count[g] > 1 {
			rep++
		}
	}
	return float64(rep) / float64(len(gs))
}

func contentWords(words []string) map[string]bool {
	m := map[string]bool{}
	for _, w := range words {
		if len([]rune(w)) >= 4 {
			m[w] = true
		}
	}
	return m
}
