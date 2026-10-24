package script

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"sleepy-agent/internal/llm"
	"sleepy-agent/internal/policy"
)

const (
	paragraphsPerChunk = 12
	wordsPerParagraph  = 56 // 4 sentences of about 14 words
)

// ErrTruncated means the model stopped because it hit the token limit
// (finish_reason "length"), so what it wrote is cut off. The text is not used:
// a script that ends mid sentence must never look finished to the next step.
// Raising MaxTokensPerChunk, or making the model reason less, is the usual fix.
var ErrTruncated = errors.New("the model stopped at the token limit")

// Config tunes the generator. Zero values pick Sleepy's defaults.
type Config struct {
	Series            string  // default "Sleepy"
	Style             string  // Cosmos, Earthside or Myth. Default Earthside
	Language          string  // en, tr, pt, es, it. Default en
	DurationMin       int     // default 5
	Temperature       float64 // default 0.7, like Sleepy
	MaxTokensPerChunk int     // default 1500, like Sleepy's floor
	// Imagery says where the images come from. "stock" (the default, and what
	// Sleepy does) gives the model the style's ten imagery lines to rotate
	// through. "topic" gives it none and asks for images from the topic's own
	// world.
	Imagery string
}

// Imagery modes.
const (
	ImageryStock = "stock"
	ImageryTopic = "topic"
	// ImageryTopicStrict also replaces the style's own imagery description,
	// which names forests, lakes and moss for Earthside, with a one line mood.
	// The Day 8 topic runs still wandered into those places.
	ImageryTopicStrict = "topic-strict"
)

// Result is a generated episode script.
type Result struct {
	Title         string
	Body          string
	Usage         llm.Usage
	Chunks        int  // model calls that wrote script text
	TitleFallback bool // true when the title call failed and the topic was used
}

// Generator writes scripts through an llm.Provider. It never retries: the
// worker owns the retry policy for pipeline steps.
type Generator struct {
	provider llm.Provider
	cfg      Config
}

// New builds a Generator.
func New(p llm.Provider, cfg Config) (*Generator, error) {
	if p == nil {
		return nil, errors.New("script: provider is required")
	}
	if cfg.Series == "" {
		cfg.Series = "Sleepy"
	}
	if cfg.Style == "" {
		cfg.Style = "Earthside"
	}
	if _, ok := styleGuides[cfg.Style]; !ok {
		return nil, fmt.Errorf("script: unknown style %q, want Cosmos, Earthside or Myth", cfg.Style)
	}
	switch cfg.Imagery {
	case "":
		cfg.Imagery = ImageryStock
	case ImageryStock, ImageryTopic, ImageryTopicStrict:
	default:
		return nil, fmt.Errorf("script: unknown imagery mode %q, want %s, %s or %s", cfg.Imagery, ImageryStock, ImageryTopic, ImageryTopicStrict)
	}
	if cfg.Language == "" {
		cfg.Language = "en"
	}
	if _, ok := langNames[cfg.Language]; !ok {
		return nil, fmt.Errorf("script: unknown language %q", cfg.Language)
	}
	if cfg.DurationMin <= 0 {
		cfg.DurationMin = 5
	}
	if cfg.Temperature <= 0 {
		cfg.Temperature = 0.7
	}
	if cfg.MaxTokensPerChunk <= 0 {
		cfg.MaxTokensPerChunk = 1500
	}
	return &Generator{provider: p, cfg: cfg}, nil
}

// Policy is the word limits this generator aims for.
func (g *Generator) Policy() policy.Policy { return policy.ForDuration(g.cfg.DurationMin) }

// plan returns how many paragraphs and chunks a target word count needs.
func plan(targetWords int) (paragraphs, chunks int) {
	paragraphs = targetWords / wordsPerParagraph
	if paragraphs < paragraphsPerChunk {
		paragraphs = paragraphsPerChunk
	}
	chunks = (paragraphs + paragraphsPerChunk - 1) / paragraphsPerChunk
	return paragraphs, chunks
}

// Mode says how an adjusted attempt differs from a first one.
type Mode string

const (
	// ModeRegenerate writes the whole script again. It is the zero value.
	ModeRegenerate Mode = ""
	// ModeContinue keeps PreviousBody and writes more paragraphs after it.
	ModeContinue Mode = "continue"
	// ModeRetitle keeps PreviousBody and only asks for a new title.
	ModeRetitle Mode = "retitle"
)

// Adjust steers one attempt. The fix engine builds it from a failed QA report;
// the zero value is a plain first attempt.
type Adjust struct {
	Mode             Mode
	Temperature      float64 // 0 keeps the configured temperature
	ExtraInstruction string  // a repair note added to the prompt
	TargetWords      int     // 0 keeps the policy target
	PreviousTitle    string  // ModeContinue keeps it, ModeRetitle falls back to it
	PreviousBody     string  // required by ModeContinue and ModeRetitle
	Style            string  // the run's own style; empty keeps the configured one
}

// Styles lists the style names the generator knows.
func Styles() []string {
	names := make([]string, 0, len(styleGuides))
	for n := range styleGuides {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// styleFor is the style of one attempt: the run's own when it has one, else
// the configured default.
func (g *Generator) styleFor(a Adjust) (string, error) {
	if a.Style == "" {
		return g.cfg.Style, nil
	}
	if _, ok := styleGuides[a.Style]; !ok {
		return "", fmt.Errorf("script: unknown style %q, want %s", a.Style, strings.Join(Styles(), ", "))
	}
	return a.Style, nil
}

// Fingerprint identifies everything that changes what this generator writes:
// the model, the settings and the prompt version. Two generators with the same
// fingerprint, given the same topic, are asked the same question. It is the
// producer half of an idempotency key.
func (g *Generator) Fingerprint() string {
	return fmt.Sprintf("%s|%s|%s|%s|%d|%.2f|%d|%s|%s", g.provider.Name(), g.cfg.Series, g.cfg.Style,
		g.cfg.Language, g.cfg.DurationMin, g.cfg.Temperature, g.cfg.MaxTokensPerChunk, g.cfg.Imagery, PromptVersion)
}

// Generate writes the script for a topic, chunk by chunk, then a title.
// On error it still returns the Usage spent so far.
func (g *Generator) Generate(ctx context.Context, topic string) (Result, error) {
	return g.GenerateAdjusted(ctx, topic, Adjust{})
}

// GenerateAdjusted is Generate steered by a fix: a different temperature, a
// repair note, another word target, or a continuation or a new title for a
// script that is otherwise kept.
func (g *Generator) GenerateAdjusted(ctx context.Context, topic string, a Adjust) (Result, error) {
	var res Result
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return res, errors.New("script: topic is required")
	}
	switch a.Mode {
	case ModeRegenerate, ModeContinue, ModeRetitle:
	default:
		return res, fmt.Errorf("script: unknown mode %q", a.Mode)
	}
	if a.Mode != ModeRegenerate && strings.TrimSpace(a.PreviousBody) == "" {
		return res, fmt.Errorf("script: mode %q needs the previous script", a.Mode)
	}
	style, err := g.styleFor(a)
	if err != nil {
		return res, err
	}

	if a.Mode == ModeRetitle {
		res.Body = a.PreviousBody
		return g.finishTitle(ctx, topic, a, res)
	}

	target := g.Policy().TargetWords
	if a.TargetWords > 0 {
		target = a.TargetWords
	}
	var totalParas, numChunks int
	var parts []string
	continuing := a.Mode == ModeContinue
	if continuing {
		missing := target - len(strings.Fields(a.PreviousBody))
		if missing < wordsPerParagraph {
			missing = wordsPerParagraph // there is always something to add
		}
		totalParas = (missing + wordsPerParagraph - 1) / wordsPerParagraph
		numChunks = (totalParas + paragraphsPerChunk - 1) / paragraphsPerChunk
		parts = append(parts, strings.TrimSpace(a.PreviousBody))
	} else {
		totalParas, numChunks = plan(target)
	}
	temp := g.cfg.Temperature
	if a.Temperature > 0 {
		temp = a.Temperature
	}
	imagery := imagerySets[style]
	styleText := styleGuides[style]
	if g.cfg.Imagery == ImageryTopic || g.cfg.Imagery == ImageryTopicStrict {
		imagery = nil
	}
	if g.cfg.Imagery == ImageryTopicStrict {
		styleText = styleMoods[style]
	}

	previous := ""
	if continuing {
		previous = lastParagraphs(a.PreviousBody, 2)
	}
	for i := 0; i < numChunks; i++ {
		paras := totalParas - i*paragraphsPerChunk
		if paras > paragraphsPerChunk {
			paras = paragraphsPerChunk
		}
		prompt, err := buildChunkPrompt(chunkData{
			ParagraphCount:   paras,
			IsFirstChunk:     i == 0 && !continuing,
			IsLastChunk:      i == numChunks-1,
			Series:           g.cfg.Series,
			Episode:          topic,
			LangName:         langNames[g.cfg.Language],
			StyleDescription: styleText,
			ImagerySets:      rotateImagery(imagery, i),
			PreviousContext:  previous,
			ChunkMinWords:    paras * 50,
			ChunkMaxWords:    paras * 65,
			ExtraInstruction: a.ExtraInstruction,
		})
		if err != nil {
			return res, fmt.Errorf("script: build prompt for chunk %d: %w", i+1, err)
		}
		resp, err := g.provider.Complete(ctx, llm.Request{
			Messages: []llm.Message{
				{Role: llm.RoleSystem, Content: systemPrompt},
				{Role: llm.RoleUser, Content: prompt},
			},
			Temperature: temp,
			MaxTokens:   g.cfg.MaxTokensPerChunk,
		})
		res.Usage = addUsage(res.Usage, resp.Usage)
		if err != nil {
			return res, fmt.Errorf("script: chunk %d of %d: %w", i+1, numChunks, err)
		}
		text := cleanChunkOutput(resp.Content)
		cutOff := resp.FinishReason == "length"
		detail := fmt.Sprintf("finish_reason %q, %d completion tokens of which %d reasoning, max_tokens %d",
			resp.FinishReason, resp.Usage.CompletionTokens, resp.ReasoningTokens, g.cfg.MaxTokensPerChunk)
		switch {
		case text == "" && cutOff:
			// The model spent the whole budget before it wrote anything, which
			// is what a reasoning model does when it thinks up to the limit.
			return res, fmt.Errorf("script: chunk %d of %d: %w, the model returned no script text (raw %d bytes, %s)",
				i+1, numChunks, ErrTruncated, len(resp.Content), detail)
		case text == "":
			return res, fmt.Errorf("script: chunk %d of %d: the model returned no script text (raw %d bytes, %s)",
				i+1, numChunks, len(resp.Content), detail)
		case cutOff:
			return res, fmt.Errorf("script: chunk %d of %d: %w (%s); the %d words it wrote are cut off and were dropped",
				i+1, numChunks, ErrTruncated, detail, len(strings.Fields(text)))
		}
		res.Chunks++
		parts = append(parts, text)
		previous = lastParagraphs(text, 2)
	}
	res.Body = strings.Join(parts, "\n\n")

	if continuing && strings.TrimSpace(a.PreviousTitle) != "" {
		res.Title = a.PreviousTitle // the title was not what failed
		return res, nil
	}
	return g.finishTitle(ctx, topic, a, res)
}

// finishTitle asks for a title and fills res.Title. A failed title call does
// not lose a finished script: the fallback is the previous title when there is
// one, the topic otherwise.
func (g *Generator) finishTitle(ctx context.Context, topic string, a Adjust, res Result) (Result, error) {
	style, err := g.styleFor(a)
	if err != nil {
		return res, err
	}
	title, usage, err := g.title(ctx, topic, style, res.Body, a)
	res.Usage = addUsage(res.Usage, usage)
	switch {
	case err == nil:
		res.Title = title
	case ctx.Err() != nil:
		return res, ctx.Err()
	default:
		// Like Sleepy, a failed title does not lose a finished script.
		res.Title, res.TitleFallback = topic, true
		if strings.TrimSpace(a.PreviousTitle) != "" {
			res.Title = a.PreviousTitle
		}
	}
	return res, nil
}

func (g *Generator) title(ctx context.Context, topic, style, body string, a Adjust) (string, llm.Usage, error) {
	sys := fmt.Sprintf(`Generate a short episode name for a sleep narration video.
Rules:
- 2-5 words maximum. Short and evocative.
- Language: %s
- Poetic, calm, imagery-based. Like a place or scene name.
- It must name the episode topic: keep its main word or words in the name.
- No generic words like "sleep", "relaxation", "journey", "peaceful".
- No clickbait, no ALL CAPS, no exclamation marks, no quotes.
- Examples: "Rings of Saturn", "The Slow Moons", "Velvet Meadow", "Amber Lanterns"
- Return ONLY the episode name, nothing else.`, langNames[g.cfg.Language])
	if note := strings.TrimSpace(a.ExtraInstruction); note != "" && a.Mode == ModeRetitle {
		sys += "\n\nREPAIR NOTE (the previous title was rejected by the quality gate):\n" + note
	}
	user := fmt.Sprintf("Series: %s\nEpisode: %s\nStyle: %s\nDuration: %d minutes\n\nScript excerpt:\n%s",
		g.cfg.Series, topic, style, g.cfg.DurationMin, excerpt(body, 500))

	temp := 0.7
	if a.Mode == ModeRetitle && a.Temperature > 0 {
		temp = a.Temperature
	}
	resp, err := g.provider.Complete(ctx, llm.Request{
		Messages:    []llm.Message{{Role: llm.RoleSystem, Content: sys}, {Role: llm.RoleUser, Content: user}},
		Temperature: temp,
		// Only a cap: a plain model stops after a few tokens. A reasoning model
		// spends hidden tokens here too, and 100 truncated it (98 reasoning
		// tokens seen), which silently fell back to the topic as the title.
		MaxTokens: g.cfg.MaxTokensPerChunk,
	})
	if err != nil {
		return "", resp.Usage, err
	}
	if resp.FinishReason == "length" {
		return "", resp.Usage, fmt.Errorf("script: title %w", ErrTruncated)
	}
	title := strings.TrimSpace(strings.Trim(strings.TrimSpace(resp.Content), "\"'"+"“”‘’"))
	if title == "" || strings.Contains(title, "\n") {
		return "", resp.Usage, errors.New("script: unusable title")
	}
	return title, resp.Usage, nil
}

func addUsage(a, b llm.Usage) llm.Usage {
	return llm.Usage{PromptTokens: a.PromptTokens + b.PromptTokens, CompletionTokens: a.CompletionTokens + b.CompletionTokens}
}

func excerpt(text string, maxWords int) string {
	words := strings.Fields(text)
	if len(words) <= maxWords {
		return text
	}
	return strings.Join(words[:maxWords], " ")
}

// splitParagraphs splits on blank lines and drops empty paragraphs.
func splitParagraphs(text string) []string {
	var out []string
	for _, p := range strings.Split(text, "\n\n") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func lastParagraphs(text string, n int) string {
	ps := splitParagraphs(text)
	if len(ps) > n {
		ps = ps[len(ps)-n:]
	}
	return strings.Join(ps, "\n\n")
}

// rotateImagery starts each chunk at a different place in the imagery list.
func rotateImagery(imagery []string, chunk int) []string {
	n := len(imagery)
	if n == 0 {
		return imagery
	}
	start := (chunk * paragraphsPerChunk) % n
	out := make([]string, n)
	for i := range out {
		out[i] = imagery[(start+i)%n]
	}
	return out
}

// cleanChunkOutput cuts an SSML section the model sometimes adds. It does
// nothing else: the rest of the output is judged as it came.
func cleanChunkOutput(raw string) string {
	md := raw
	for _, sep := range []string{"---SSML---", "---\nSSML---", "---\n\nSSML---", "--- SSML ---", "---SSML ---"} {
		if idx := strings.Index(md, sep); idx >= 0 {
			md = md[:idx]
			break
		}
	}
	return strings.TrimSpace(md)
}
