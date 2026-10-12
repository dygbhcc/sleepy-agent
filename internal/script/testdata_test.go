package script

import (
	"fmt"
	"strings"
)

// cleanParagraph is 4 sentences of 12 words each, so 48 words.
func cleanParagraph(i int) string {
	s := func(j int) string {
		return fmt.Sprintf("The slow river %d bends through the quiet meadow part %d tonight.", i, j)
	}
	return strings.Join([]string{s(1), s(2), s(3), s(4)}, " ")
}

// cleanScript has n paragraphs that follow every rule except length.
func cleanScript(n int) string {
	ps := make([]string, n)
	for i := range ps {
		ps[i] = cleanParagraph(i)
	}
	return strings.Join(ps, "\n\n")
}
