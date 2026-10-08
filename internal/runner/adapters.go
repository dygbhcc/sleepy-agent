package runner

import (
	"context"

	"sleepy-agent/internal/llm"
	"sleepy-agent/internal/pipeline"
)

// Every external capability is an interface. Mocks live in this repo and tests
// never touch the network or a paid API. Real adapters are ported from Sleepy
// on Day 10, after QA gates (Day 6) and guardrails (Day 9) exist.

// Script is what the script step produces.
type Script struct {
	Title string
	Body  string
}

// Scripter writes the episode script for a topic.
type Scripter interface {
	Script(ctx context.Context, topic string) (Script, error)
}

// Voice turns a script into narration audio and returns a reference to it.
type Voice interface {
	Synthesize(ctx context.Context, script string) (ref string, err error)
}

// Renderer produces the visual side of an episode. Each method returns a
// reference to the file it made.
type Renderer interface {
	Thumbnail(ctx context.Context, title string) (ref string, err error)
	Render(ctx context.Context, voiceRef, thumbnailRef string) (ref string, err error)
	Package(ctx context.Context, videoRef string) (ref string, err error)
}

// PublishRequest is everything a Publisher needs. It carries the run ID so a
// publisher can be idempotent.
type PublishRequest struct {
	RunID    string
	Title    string
	VideoRef string
}

// PublishResult says what the Publisher did.
type PublishResult struct {
	ID         string
	Visibility string // "dry-run", "unlisted", "public"
}

// Publisher puts a finished episode where viewers can see it.
type Publisher interface {
	Publish(ctx context.Context, req PublishRequest) (PublishResult, error)
}

// DryRunPublisher is the default Publisher. It reports what it would do and
// publishes nothing. A real publisher has to be chosen on purpose.
type DryRunPublisher struct{}

// Publish implements Publisher.
func (DryRunPublisher) Publish(_ context.Context, req PublishRequest) (PublishResult, error) {
	return PublishResult{ID: "dry-run:" + req.RunID, Visibility: "dry-run"}, nil
}

// LLMScripter writes scripts with an llm.Provider, using the naive Day 2
// pipeline. Day 5 replaces it with the ported script generator, Day 6 adds QA gates.
type LLMScripter struct {
	Provider llm.Provider
}

// Script implements Scripter.
func (s LLMScripter) Script(ctx context.Context, topic string) (Script, error) {
	ep, err := pipeline.Run(ctx, s.Provider, topic)
	if err != nil {
		return Script{}, err
	}
	return Script{Title: ep.Title, Body: ep.Script}, nil
}
