package runner

import (
	"context"

	"sleepy-agent/internal/llm"
	"sleepy-agent/internal/script"
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

// LLMScripter writes scripts with an llm.Provider, using the generator ported
// from Sleepy (internal/script). Day 6 adds QA gates after it.
type LLMScripter struct {
	Provider llm.Provider
	Config   script.Config // zero value: 5 minute Earthside episode in English
}

// Script implements Scripter.
func (s LLMScripter) Script(ctx context.Context, topic string) (Script, error) {
	g, err := script.New(s.Provider, s.Config)
	if err != nil {
		return Script{}, err
	}
	res, err := g.Generate(ctx, topic)
	if err != nil {
		return Script{}, err
	}
	return Script{Title: res.Title, Body: res.Body}, nil
}
