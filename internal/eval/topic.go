package eval

import (
	"fmt"
	"regexp"
	"strings"
)

// A run's topic decides its theme: the video name and the background follow
// it. These measures say whether the script and the title still carry it.
// They look at words only. A script can use a topic's words and still be
// off topic, and a title can paraphrase the topic and miss every word, so a
// concern here is a reason to look, not a verdict.

var topicStopWords = set(strings.Fields("the a an of at on in and to for with by from"))

// stem drops one trailing "s", so "lanterns" matches "lantern".
func stem(w string) string {
	if len(w) > 3 && strings.HasSuffix(w, "s") {
		return w[:len(w)-1]
	}
	return w
}

// TopicWords are the topic's words that carry meaning: no articles or
// prepositions, each reduced by stem, in order and without repeats.
func TopicWords(topic string) []string {
	var out []string
	seen := map[string]bool{}
	for _, w := range tokens(topic) {
		if topicStopWords[w] {
			continue
		}
		w = stem(w)
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}

// TopicCoverage reports how many of the topic's words appear in text at least
// once, out of how many there are, and how many times any of them appears.
func TopicCoverage(topic, text string) (covered, of, mentions int) {
	words := TopicWords(topic)
	count := map[string]int{}
	for _, t := range tokens(text) {
		count[stem(t)]++
	}
	for _, w := range words {
		if n := count[w]; n > 0 {
			covered++
			mentions += n
		}
	}
	return covered, len(words), mentions
}

// maxTopicDensity is how many times per 100 words a script may use the topic's
// words before it reads as a refrain. A first guess from a handful of scripts:
// the corrected Day 8 run had 0.7 to 2.7 for scripts that read naturally, and
// 5 to 8 for scripts that open sentences with the topic's name.
const maxTopicDensity = 4.0

// TopicDensity is the mentions of the topic's words per 100 words of text.
func TopicDensity(topic, text string) float64 {
	words := len(strings.Fields(text))
	if words == 0 {
		return 0
	}
	_, _, n := TopicCoverage(topic, text)
	return float64(n) * 100 / float64(words)
}

var metaRe = regexp.MustCompile(`(?i)\b(this|the|our) (episode|video|script)\b`)

// MetaSentences counts sentences in which the narrator talks about the
// episode itself, such as "The episode returns to the slow moons". The
// prompt asks the model to come back to the topic, and it can do that out of
// the story instead of inside it.
func MetaSentences(body string) int {
	n := 0
	for _, s := range strings.FieldsFunc(body, func(r rune) bool { return r == '.' || r == '!' || r == '?' }) {
		if metaRe.MatchString(s) {
			n++
		}
	}
	return n
}

// TopicConcerns lists what is wrong with how a script and its title carry the
// topic. A topic with no meaningful words has nothing to check.
func TopicConcerns(topic, title, body string) []string {
	if len(TopicWords(topic)) == 0 {
		return nil
	}
	var c []string
	// Part of a topic is not a concern: "Velvet Meadow" can be told without
	// the word velvet. Only a script that carries none of it is.
	if cov, _, _ := TopicCoverage(topic, body); cov == 0 {
		c = append(c, "none of the topic's words appear in the script")
	}
	if d := TopicDensity(topic, body); d > maxTopicDensity {
		c = append(c, fmt.Sprintf("the topic's words appear %.1f times per 100 words (max %.0f)", d, maxTopicDensity))
	}
	if n := MetaSentences(body); n > 0 {
		c = append(c, fmt.Sprintf("%d sentences talk about the episode itself", n))
	}
	if strings.TrimSpace(title) != "" {
		if cov, _, _ := TopicCoverage(topic, title); cov == 0 {
			c = append(c, "the title keeps none of the topic's words")
		}
	}
	return c
}
