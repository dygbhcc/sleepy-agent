package eval

import (
	"fmt"

	"sleepy-agent/internal/script"
)

// anchorMatches reports whether a word is the anchor or the anchor with a
// short ending, so "firefl" matches fireflies and "librar" matches libraries.
func anchorMatches(word, anchor string) bool {
	return word == anchor || (len(word) > len(anchor) && len(word) <= len(anchor)+3 && word[:len(anchor)] == anchor)
}

// StockImageryUsed counts how many of the style's ten imagery lines show up in
// a script through their anchor word, out of how many could be counted. An
// anchor that is also a word of the topic is left out: a script about snow
// should say snow. The prompt asks the model to treat the lines as themes, and
// a script that touches nearly all of them is a tour of the list, not a piece
// about its topic.
func StockImageryUsed(topic, body, style string) (used, of int) {
	skip := map[string]bool{}
	for _, w := range TopicWords(topic) {
		skip[w] = true
	}
	words := tokens(body)
	for _, a := range script.ImageryAnchors(style) {
		covered := false
		for w := range skip {
			if anchorMatches(w, a) || anchorMatches(a, w) {
				covered = true
			}
		}
		if covered {
			continue
		}
		of++
		for _, w := range words {
			if anchorMatches(w, a) {
				used++
				break
			}
		}
	}
	return used, of
}

// walksTheList is true when a script uses nine tenths of the countable
// imagery lines. A first guess: in the Day 8 runs every Earthside script used
// 9 or 10 of 10.
func walksTheList(used, of int) bool { return of >= 5 && used*10 >= of*9 }

// StockImageryConcern is a concern when the script walks through the list.
func StockImageryConcern(topic, body, style string) []string {
	used, of := StockImageryUsed(topic, body, style)
	if walksTheList(used, of) {
		return []string{fmt.Sprintf("touches %d of %d images from the prompt's stock list", used, of)}
	}
	return nil
}
