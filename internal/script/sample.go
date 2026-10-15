package script

import (
	"fmt"
	"strings"
)

// SampleScript returns a deterministic script of n paragraphs that follows
// every rule: 4 sentences of 13 words per paragraph, no banned words, no
// repeated sentence. With n = 12 it has 624 words, inside the limits of a 5
// minute episode.
//
// It exists for mocks and demos (the walking skeleton, runner tests), so they
// can feed a gate a script that should pass. It is not a model output and
// measures nothing.
func SampleScript(n int) string {
	paras := make([]string, n)
	for i := range paras {
		sents := make([]string, 4)
		for j := range sents {
			k := i*4 + j
			sents[j] = fmt.Sprintf("The slow river bends past stone%d and pool%d near the quiet meadow tonight.", k, k)
		}
		paras[i] = strings.Join(sents, " ")
	}
	return strings.Join(paras, "\n\n")
}
