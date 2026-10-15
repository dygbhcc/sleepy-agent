package script

import (
	"context"
	"errors"
	"fmt"
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
}

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

// Generate writes the script for a topic, chunk by chunk, then a title.
// On error it still returns the Usage spent so far.
func (g *Generator) Generate(ctx context.Context, topic string) (Result, error) {
	var res Result
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return res, errors.New("script: topic is required")
	}
	totalParas, numChunks := plan(g.Policy().TargetWords)
	imagery := imagerySets[g.cfg.Style]

	var parts []string
	var previous string
	for i := 0; i < numChunks; i++ {
		paras := totalParas - i*paragraphsPerChunk
		if paras > paragraphsPerChunk {
			paras = paragraphsPerChunk
		}
		prompt, err := buildChunkPrompt(chunkData{
			ParagraphCount:   paras,
			IsFirstChunk:     i == 0,
			IsLastChunk:      i == numChunks-1,
			Series:           g.cfg.Series,
			Episode:          topic,
			LangName:         langNames[g.cfg.Language],
			StyleDescription: styleGuides[g.cfg.Style],
			ImagerySets:      rotateImagery(imagery, i),
			PreviousContext:  previous,
			ChunkMinWords:    paras * 50,
			ChunkMaxWords:    paras * 65,
		})
		if err != nil {
			return res, fmt.Errorf("script: build prompt for chunk %d: %w", i+1, err)
		}
		resp, err := g.provider.Complete(ctx, llm.Request{
			Messages: []llm.Message{
				{Role: llm.RoleSystem, Content: systemPrompt},
				{Role: llm.RoleUser, Content: prompt},
			},
			Temperature: g.cfg.Temperature,
			MaxTokens:   g.cfg.MaxTokensPerChunk,
		})
		res.Usage = addUsage(res.Usage, resp.Usage)
		if err != nil {
			return res, fmt.Errorf("script: chunk %d of %d: %w", i+1, numChunks, err)
		}
		text := cleanChunkOutput(resp.Content)
		if text == "" {
			return res, fmt.Errorf("script: chunk %d of %d: the model returned no script text (raw %d bytes, finish_reason %q, %d completion tokens of which %d reasoning, max_tokens %d)",
				i+1, numChunks, len(resp.Content), resp.FinishReason, resp.Usage.CompletionTokens, resp.ReasoningTokens, g.cfg.MaxTokensPerChunk)
		}
		if resp.FinishReason == "length" {
			return res, fmt.Errorf("script: chunk %d of %d: %w (finish_reason %q, %d completion tokens of which %d reasoning, max_tokens %d); the %d words it wrote are cut off and were dropped",
				i+1, numChunks, ErrTruncated, resp.FinishReason, resp.Usage.CompletionTokens, resp.ReasoningTokens, g.cfg.MaxTokensPerChunk, len(strings.Fields(text)))
		}
		res.Chunks++
		parts = append(parts, text)
		previous = lastParagraphs(text, 2)
	}
	res.Body = strings.Join(parts, "\n\n")

	title, usage, err := g.title(ctx, topic, res.Body)
	res.Usage = addUsage(res.Usage, usage)
	switch {
	case err == nil:
		res.Title = title
	case ctx.Err() != nil:
		return res, ctx.Err()
	default:
		// Like Sleepy, a failed title does not lose a finished script.
		res.Title, res.TitleFallback = topic, true
	}
	return res, nil
}

func (g *Generator) title(ctx context.Context, topic, body string) (string, llm.Usage, error) {
	sys := fmt.Sprintf(`Generate a short episode name for a sleep narration video.
Rules:
- 2-5 words maximum. Short and evocative.
- Language: %s
- Poetic, calm, imagery-based. Like a place or scene name.
- No generic words like "sleep", "relaxation", "journey", "peaceful".
- No clickbait, no ALL CAPS, no exclamation marks, no quotes.
- Examples: "Rings of Saturn", "The Slow Moons", "Velvet Meadow", "Amber Lanterns"
- Return ONLY the episode name, nothing else.`, langNames[g.cfg.Language])
	user := fmt.Sprintf("Series: %s\nEpisode: %s\nStyle: %s\nDuration: %d minutes\n\nScript excerpt:\n%s",
		g.cfg.Series, topic, g.cfg.Style, g.cfg.DurationMin, excerpt(body, 500))

	resp, err := g.provider.Complete(ctx, llm.Request{
		Messages:    []llm.Message{{Role: llm.RoleSystem, Content: sys}, {Role: llm.RoleUser, Content: user}},
		Temperature: 0.7,
		MaxTokens:   100,
	})
	if err != nil {
		return "", resp.Usage, err
	}
	title := strings.TrimSpace(strings.Trim(strings.TrimSpace(resp.Content), "\"'“”‘’"))
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
