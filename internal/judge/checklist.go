package judge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"sleepy-agent/internal/eval"
	"sleepy-agent/internal/llm"
)

// Checklist is the second judge: it does not score, it checks a script against
// the defects in docs/quality-spec.md that need reading, not counting. For each
// defect the model answers yes or no with verbatim quotes. Code checks every
// quote against the script, counts the valid ones and applies the spec's reject
// thresholds, so the model never decides whether a script passes.
//
// Like Judge it only measures. It earns a vote after its findings are compared
// with a human's labels.

// Mode says what an unsure answer means (spec section 6a).
type Mode string

const (
	// ModeOwnerReviews is today: the owner reads every script, so unsure is
	// the same as not found.
	ModeOwnerReviews Mode = "owner_reviews"
	// ModeAutonomous is for when nobody reads every script: unsure sends the
	// script to review.
	ModeAutonomous Mode = "autonomous"
)

// ChecklistIDs are the defects this checker answers, in the order it is asked.
var ChecklistIDs = []string{"H2b", "H4", "H5a", "H5b", "H5c", "H6", "H7", "H10"}

const minQuoteRunes = 8

// Checklist checks scripts with a model behind llm.Provider.
type Checklist struct {
	Provider  llm.Provider
	MaxTokens int // reply budget, 0 means 4000
	Mode      Mode
	// NoConfirm turns the second pass off. By default a defect is kept only if
	// a second pass, asked about the reported defects alone, returns at least
	// one of the same quotes.
	NoConfirm bool
	// ConfirmByPresence changes what the second pass must agree on. By default
	// it must return one of the same quotes, which drops a real defect that has
	// several sentences when the two passes pick different ones. With this set,
	// a defect stays, with the first pass's valid quotes, when the second pass
	// also says it is present. The default is unchanged until this is measured
	// on scripts it was not tuned on.
	ConfirmByPresence bool
}

// ChecklistResult is what the checklist found in one script.
type ChecklistResult struct {
	Findings []eval.Finding
	// Dropped lists defects the model reported without a quote that is in the
	// script: they count as not found.
	Dropped []string
	// Trace says, per defect, what the model answered and how many of its
	// quotes were word for word in the script. It is for diagnosis only.
	Trace            []string
	PromptTokens     int
	CompletionTokens int
}

const checklistPrompt = `You check a sleep narration script against a list of defects. You do not give opinions or scores.
The script is split into paragraphs marked [P1], [P2], and so on. Never put a marker in a quote.
For each defect id below, in order, answer in one JSON object:
{"checks": [{"id": "H5a", "present": true|false, "unsure": true|false, "clear": true|false, "count": 0, "markers": [{"par": 3, "quote": "..."}], "quotes": ["..."]}, ...]}
Rules:
- The default is present false. present true needs at least one verbatim quote copied from the script, word for word.
- quotes lists every sentence or phrase that shows the defect (up to 12). markers lists what you found, with its paragraph number, for H5a, H5b, H6 and H10 before you decide.
- unsure true means you cannot decide. Then present is false.
- clear true only for H6 when a statement plainly contradicts itself.
- count is used only for H10: how many paragraphs have no image tied to the topic.
- Never rewrite the script or suggest changes. Return only the JSON object.

Defects:
H2b dangling modifier: a sentence ends on a bare adjective or adverb that breaks the word order, often repeating a word from earlier in the sentence ("silvered stones gentle"). Not a defect: a clearly poetic inversion.
H4 subject missing: the body does not show the topic's subject with concrete imagery. Quote the generic passage that stands in for it. Not a defect: a subject carried by its imagery without its name (a library told through shelves, books and pages).
H5a season or weather conflict: list every weather, season and plant or animal marker, then mark conflicts. Incompatible: snow or ice with dew, fireflies, green leaves in rain, wildflowers, warm dusk air, fish, frogs, lily pads, crickets; fireflies, crickets, frogs or night-blooming jasmine with snow, frost, ice, bare branches. Quote both sides of a conflict. Not a defect: metaphors that do not state weather.
H5b time regression: list every time of day marker in order. The night must only deepen. A return to morning, dew, sunrise or dawn after night began is a regression. Quote the regressing sentence. Not a defect: one night going from dusk to deep night, or a final sentence that only hints at dawn.
H5c place incoherence: places that cannot coexist (a lighthouse on a coast with a birch forest, a snowy field and a meadow lake in the same scene). Quote two contradicting places. Not a defect: fantasy moving between places while one motif carries through.
H6 physical nonsense: in space settings, wind, breeze, scent, hum, sound or warmth stated as real in a vacuum; or a contradiction such as cold vacuum next to a warm blanket. Not a defect: poetic metaphor that claims no physical sensation.
H7 threat or tension: hunter and prey in the same script (fox and rabbit, owl and mouse), watching or stalking, sudden events, danger. Quote each such sentence. Not a defect: a sleeping dragon, a gentle giant, a sleeping owl, a statue of an owl.
H10 imported imagery: imagery recycled from settings the topic does not support (a lighthouse script whose content is birch, moss, snow, lake and rain). Quote examples. Not a defect: one or two shared sensory images such as moonlight or mist.`

type rawMarker struct {
	Par   int    `json:"par"`
	Quote string `json:"quote"`
}

type rawCheck struct {
	ID      string      `json:"id"`
	Present bool        `json:"present"`
	Unsure  bool        `json:"unsure"`
	Clear   bool        `json:"clear"`
	Count   int         `json:"count"`
	Markers []rawMarker `json:"markers"`
	Quotes  []string    `json:"quotes"`
}

type rawReply struct {
	Checks []rawCheck `json:"checks"`
}

// Review checks one script.
func (c Checklist) Review(ctx context.Context, topic, title, body string) (ChecklistResult, error) {
	if c.Provider == nil {
		return ChecklistResult{}, errors.New("checklist: no provider")
	}
	paras := eval.SpecParagraphs(body)
	var numbered []string
	for i, p := range paras {
		numbered = append(numbered, fmt.Sprintf("[P%d] %s", i+1, p))
	}
	user := fmt.Sprintf("Topic: %s\nTitle: %s\n\nScript:\n%s", topic, title, strings.Join(numbered, "\n\n"))

	var res ChecklistResult
	first, err := c.ask(ctx, checklistPrompt, user, &res)
	if err != nil {
		return res, err
	}
	checks := map[string]rawCheck{}
	for _, ck := range first.Checks {
		checks[ck.ID] = ck
	}
	// confirmNote says, per defect, what the first pass gave and what the
	// confirm pass added. It only feeds Trace.
	confirmNote := map[string]string{}
	if !c.NoConfirm {
		var ids []string
		for _, id := range ChecklistIDs {
			if ck, ok := checks[id]; ok && ck.Present {
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			second, err := c.ask(ctx, checklistPrompt+"\n\nCheck again, carefully. Answer only these defects: "+strings.Join(ids, ", ")+".", user, &res)
			if err != nil {
				return res, err
			}
			again := map[string]rawCheck{}
			for _, ck := range second.Checks {
				again[ck.ID] = ck
			}
			for _, id := range ids {
				ck := checks[id]
				confirmNote[id] = fmt.Sprintf(" first=%d confirm_present=%v confirm_quotes=%d", len(ck.Quotes), again[id].Present, len(again[id].Quotes))
				if !c.ConfirmByPresence {
					ck.Quotes = intersectQuotes(ck.Quotes, again[id].Quotes)
				}
				if !again[id].Present {
					ck.Quotes = nil
				}
				checks[id] = ck
			}
		}
	}

	sents := eval.SpecSentences(body)
	for _, id := range ChecklistIDs {
		ck, ok := checks[id]
		if !ok {
			res.Trace = append(res.Trace, id+" not answered")
			continue
		}
		valid := validQuotes(ck.Quotes, body)
		res.Trace = append(res.Trace, fmt.Sprintf("%s present=%v unsure=%v quotes=%d valid=%d", id, ck.Present, ck.Unsure, len(ck.Quotes), len(valid))+confirmNote[id])
		switch {
		case ck.Present && len(valid) == 0:
			res.Dropped = append(res.Dropped, id)
		case ck.Present:
			res.Findings = append(res.Findings, thresholdFinding(id, ck, valid, sents, len(paras)))
		case ck.Unsure && c.Mode == ModeAutonomous:
			res.Findings = append(res.Findings, eval.Finding{ID: id, Unsure: true})
		}
	}
	return res, nil
}

func (c Checklist) ask(ctx context.Context, system, user string, res *ChecklistResult) (rawReply, error) {
	max := c.MaxTokens
	if max <= 0 {
		max = 4000
	}
	resp, err := c.Provider.Complete(ctx, llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: system},
			{Role: llm.RoleUser, Content: user},
		},
		Temperature: 0,
		MaxTokens:   max,
		JSON:        true,
	})
	res.PromptTokens += resp.Usage.PromptTokens
	res.CompletionTokens += resp.Usage.CompletionTokens
	if err != nil {
		return rawReply{}, err
	}
	if resp.FinishReason == "length" {
		return rawReply{}, fmt.Errorf("%w (%d completion tokens of which %d reasoning, max_tokens %d)",
			ErrTruncated, resp.Usage.CompletionTokens, resp.ReasoningTokens, max)
	}
	i, k := strings.Index(resp.Content, "{"), strings.LastIndex(resp.Content, "}")
	if i < 0 || k < i {
		return rawReply{}, fmt.Errorf("%w: no JSON object in %q", ErrBadVerdict, clip(resp.Content))
	}
	var r rawReply
	if err := json.Unmarshal([]byte(resp.Content[i:k+1]), &r); err != nil {
		return rawReply{}, fmt.Errorf("%w: %v", ErrBadVerdict, err)
	}
	return r, nil
}

func normQuote(s string) string {
	s = strings.NewReplacer("’", "'", "‘", "'", "“", `"`, "”", `"`, "‑", "-").Replace(s)
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// validQuotes keeps the quotes that are word for word in the script, without
// repeats. A quote that is not in the script is the model's invention.
func validQuotes(quotes []string, body string) []string {
	nb := normQuote(body)
	seen := map[string]bool{}
	var out []string
	for _, q := range quotes {
		nq := normQuote(strings.Trim(strings.TrimSpace(q), `.`))
		if len([]rune(nq)) < minQuoteRunes || seen[nq] || !strings.Contains(nb, nq) {
			continue
		}
		seen[nq] = true
		out = append(out, strings.TrimSpace(q))
	}
	return out
}

func intersectQuotes(a, b []string) []string {
	in := map[string]bool{}
	for _, q := range b {
		in[normQuote(q)] = true
	}
	var out []string
	for _, q := range a {
		if in[normQuote(q)] {
			out = append(out, q)
		}
	}
	return out
}

// sentenceIndexes finds the sentences a quote touches.
func sentenceIndexes(quotes, sents []string) []int {
	set := map[int]bool{}
	for _, q := range quotes {
		nq := normQuote(strings.Trim(q, "."))
		for i, s := range sents {
			ns := normQuote(strings.Trim(s, "."))
			if ns != "" && (strings.Contains(ns, nq) || strings.Contains(nq, ns)) {
				set[i] = true
			}
		}
	}
	out := make([]int, 0, len(set))
	for i := range set {
		out = append(out, i)
	}
	sort.Ints(out)
	return out
}

// thresholdFinding applies the spec's reject threshold for one defect to the
// valid quotes.
func thresholdFinding(id string, ck rawCheck, valid, sents []string, paragraphs int) eval.Finding {
	idx := sentenceIndexes(valid, sents)
	f := eval.Finding{ID: id, Count: len(valid), Quote: valid[0], Sentences: idx}
	if len(sents) > 0 {
		f.Share = float64(len(valid)) / float64(len(sents))
	}
	n := len(valid)
	switch id {
	case "H2b":
		f.Reject = n >= 6
	case "H4":
		f.Reject = true
	case "H5a", "H5c":
		f.Reject = n >= 2 // one conflict needs both sides quoted
	case "H5b":
		onlyLast := len(sents) > 0 && len(idx) == 1 && idx[0] == len(sents)-1
		f.Reject = !onlyLast
	case "H6":
		f.Reject = n >= 2 || ck.Clear
	case "H7":
		f.Reject = n >= 3
	case "H10":
		f.Reject = paragraphs > 0 && ck.Count*2 > paragraphs
	}
	f.Warn = !f.Reject
	return f
}
