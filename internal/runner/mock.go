package runner

import (
	"context"
	"fmt"
	"sync"
)

// failQueue is the "fail the next N calls" behaviour every mock shares.
type failQueue struct {
	mu    sync.Mutex
	calls int
	fail  []error
}

// next counts a call and returns the queued error for it, if any.
func (q *failQueue) next() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.calls++
	if len(q.fail) == 0 {
		return nil
	}
	err := q.fail[0]
	q.fail = q.fail[1:]
	return err
}

func (q *failQueue) failNext(errs ...error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.fail = append(q.fail, errs...)
}

func (q *failQueue) count() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.calls
}

// MockVoice is a Voice that makes up a reference. Safe for concurrent use.
type MockVoice struct{ q failQueue }

// FailNext queues errors for the next calls.
func (m *MockVoice) FailNext(errs ...error) { m.q.failNext(errs...) }

// Calls is how many times Synthesize was called.
func (m *MockVoice) Calls() int { return m.q.count() }

// Synthesize implements Voice.
func (m *MockVoice) Synthesize(ctx context.Context, script string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := m.q.next(); err != nil {
		return "", err
	}
	return fmt.Sprintf("mock://narration/%d-words", wordCount(script)), nil
}

// MockRenderer is a Renderer that makes up references. Safe for concurrent use.
type MockRenderer struct {
	thumb, render, pack failQueue
}

// FailNextThumbnail, FailNextRender and FailNextPackage queue errors per method.
func (m *MockRenderer) FailNextThumbnail(errs ...error) { m.thumb.failNext(errs...) }
func (m *MockRenderer) FailNextRender(errs ...error)    { m.render.failNext(errs...) }
func (m *MockRenderer) FailNextPackage(errs ...error)   { m.pack.failNext(errs...) }

// ThumbnailCalls, RenderCalls and PackageCalls count calls per method.
func (m *MockRenderer) ThumbnailCalls() int { return m.thumb.count() }
func (m *MockRenderer) RenderCalls() int    { return m.render.count() }
func (m *MockRenderer) PackageCalls() int   { return m.pack.count() }

// Thumbnail implements Renderer.
func (m *MockRenderer) Thumbnail(ctx context.Context, title string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := m.thumb.next(); err != nil {
		return "", err
	}
	return "mock://thumbnail.png", nil
}

// Render implements Renderer.
func (m *MockRenderer) Render(ctx context.Context, voiceRef, thumbnailRef string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := m.render.next(); err != nil {
		return "", err
	}
	return "mock://video.mp4", nil
}

// Package implements Renderer.
func (m *MockRenderer) Package(ctx context.Context, videoRef string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := m.pack.next(); err != nil {
		return "", err
	}
	return "mock://episode-pack.zip", nil
}

// MockPublisher records every publish request. Tests use it to prove that a
// publish did not happen. Safe for concurrent use.
type MockPublisher struct {
	q        failQueue
	mu       sync.Mutex
	requests []PublishRequest
}

// FailNext queues errors for the next calls.
func (m *MockPublisher) FailNext(errs ...error) { m.q.failNext(errs...) }

// Calls is how many times Publish was called.
func (m *MockPublisher) Calls() int { return m.q.count() }

// Requests returns a copy of every request seen so far.
func (m *MockPublisher) Requests() []PublishRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]PublishRequest(nil), m.requests...)
}

// Publish implements Publisher. It reports "dry-run": a mock never publishes.
func (m *MockPublisher) Publish(ctx context.Context, req PublishRequest) (PublishResult, error) {
	if err := ctx.Err(); err != nil {
		return PublishResult{}, err
	}
	m.mu.Lock()
	m.requests = append(m.requests, req)
	m.mu.Unlock()
	if err := m.q.next(); err != nil {
		return PublishResult{}, err
	}
	return PublishResult{ID: "mock:" + req.RunID, Visibility: "dry-run"}, nil
}
