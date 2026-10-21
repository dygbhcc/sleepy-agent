// Package judge is the shadow reviewer: a language model that reads an
// accepted script and scores it, without any power over the run.
//
// The QA gate is deterministic and cheap, and it checks little. The judge looks
// at what a gate cannot count: whether the images fit the topic, whether the
// text keeps bringing something new, whether it is calm. It is a shadow
// because its verdict is recorded and never acted on. A reviewer earns a vote
// only after its verdicts have been compared with a human's (cmd/evalstats),
// and until then a wrong verdict costs nothing.
//
// The pass rule is code, not the model's opinion: the model gives scores, and
// the verdict passes when no score is under MinScore.
package judge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"sleepy-agent/internal/llm"
)

// Scores are 1 (bad) to 5 (good).
const (
	minScore     = 1
	maxScore     = 5
	DefaultMin   = 3
	defaultToken = 2000
)

// ErrTruncated means the judge hit its token limit before it answered.
var ErrTruncated = errors.New("the judge stopped at the token limit")

// ErrBadVerdict means the judge answered, but not with scores this package can read.
var ErrBadVerdict = errors.New("the judge's reply is not a valid verdict")

// Verdict is what the judge said about one script.
type Verdict struct {
	Calm      int    `json:"calm"`      // tone and imagery are gentle and suit sleep
	Coherence int    `json:"coherence"` // one piece about the topic, images that belong in it
	Variety   int    `json:"variety"`   // new images keep arriving, nothing pasted or looped
	Reason    string `json:"reason"`    // one sentence, the main weakness or "none"

	// Pass is decided by MinScore, not by the model.
	Pass     bool `json:"pass"`
	MinScore int  `json:"min_score"`

	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
}

// Judge scores scripts with a model behind llm.Provider.
type Judge struct {
	Provider llm.Provider
	// MaxTokens is the reply budget. A reasoning model spends part of it on
	// hidden reasoning, so it is generous. 0 means 2000.
	MaxTokens int
	// MinScore is the lowest score that still passes. 0 means 3.
	MinScore int
}

const systemPrompt = `You review scripts for a sleep narration channel. You do not rewrite anything.
Score the script from 1 (bad) to 5 (good) on three things:
calm: the tone and imagery are gentle and suit falling asleep.
coherence: it reads as one piece about the topic, and its images belong in it. A script about a library that keeps describing unrelated forests and rivers scores low.
variety: new images keep arriving. A phrase or image that repeats, or a list of stock images pasted into most paragraphs, scores low.
Answer with one JSON object and nothing else:
{"calm": 1-5, "coherence": 1-5, "variety": 1-5, "reason": "one sentence naming the main weakness, or none"}`

// Review scores one script.
func (j Judge) Review(ctx context.Context, topic, title, body string) (Verdict, error) {
	if j.Provider == nil {
		return Verdict{}, errors.New("judge: no provider")
	}
	max := j.MaxTokens
	if max <= 0 {
		max = defaultToken
	}
	min := j.MinScore
	if min <= 0 {
		min = DefaultMin
	}
	resp, err := j.Provider.Complete(ctx, llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: systemPrompt},
			{Role: llm.RoleUser, Content: fmt.Sprintf("Topic: %s\nTitle: %s\n\nScript:\n%s", topic, title, body)},
		},
		Temperature: 0,
		MaxTokens:   max,
		JSON:        true,
	})
	if err != nil {
		return Verdict{}, err
	}
	if resp.FinishReason == "length" {
		return Verdict{}, fmt.Errorf("%w (%d completion tokens of which %d reasoning, max_tokens %d)",
			ErrTruncated, resp.Usage.CompletionTokens, resp.ReasoningTokens, max)
	}
	v, err := parse(resp.Content)
	if err != nil {
		return Verdict{}, err
	}
	v.MinScore = min
	v.Pass = v.Calm >= min && v.Coherence >= min && v.Variety >= min
	v.PromptTokens, v.CompletionTokens = resp.Usage.PromptTokens, resp.Usage.CompletionTokens
	return v, nil
}

// parse reads the first JSON object in a reply and checks the scores. Pass and
// MinScore in the reply are ignored: Review sets them.
func parse(content string) (Verdict, error) {
	i, k := strings.Index(content, "{"), strings.LastIndex(content, "}")
	if i < 0 || k < i {
		return Verdict{}, fmt.Errorf("%w: no JSON object in %q", ErrBadVerdict, clip(content))
	}
	var v Verdict
	if err := json.Unmarshal([]byte(content[i:k+1]), &v); err != nil {
		return Verdict{}, fmt.Errorf("%w: %v", ErrBadVerdict, err)
	}
	for name, s := range map[string]int{"calm": v.Calm, "coherence": v.Coherence, "variety": v.Variety} {
		if s < minScore || s > maxScore {
			return Verdict{}, fmt.Errorf("%w: %s score %d is outside %d to %d", ErrBadVerdict, name, s, minScore, maxScore)
		}
	}
	v.Reason = strings.TrimSpace(v.Reason)
	return v, nil
}

func clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 120 {
		s = s[:120] + "..."
	}
	return s
}
