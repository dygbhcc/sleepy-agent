package llm

import (
	"context"
	"errors"
	"strings"
	"sync"
)

// ErrScriptExhausted is returned when a scripted mock has no answers left.
var ErrScriptExhausted = errors.New("llm mock: script exhausted")

// Mock is a deterministic Provider for tests and keyless demos.
//
// It answers from a fixed script, in order. It records every request so
// tests can assert on what the agent actually sent. It is safe for
// concurrent use, because the concurrency experiment (bonus week) will
// reuse it.
type Mock struct {
	mu       sync.Mutex
	script   []string
	next     int
	requests []Request
	failNext []error
}

// NewMock returns a Mock that answers with the given responses, in order.
func NewMock(script ...string) *Mock {
	return &Mock{script: script}
}

// Name implements Provider.
func (m *Mock) Name() string { return "mock" }

// FailNext queues errors that the next calls return before any scripted
// answer is consumed. Later days use it to simulate flaky providers.
func (m *Mock) FailNext(errs ...error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failNext = append(m.failNext, errs...)
}

// Requests returns a copy of every request seen so far.
func (m *Mock) Requests() []Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Request, len(m.requests))
	copy(out, m.requests)
	return out
}

// Complete implements Provider.
func (m *Mock) Complete(ctx context.Context, req Request) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, req)

	if len(m.failNext) > 0 {
		err := m.failNext[0]
		m.failNext = m.failNext[1:]
		return Response{}, err
	}
	if m.next >= len(m.script) {
		return Response{}, ErrScriptExhausted
	}
	out := m.script[m.next]
	m.next++

	return Response{
		Content: out,
		Usage: Usage{
			PromptTokens:     approxTokens(req),
			CompletionTokens: len(strings.Fields(out)),
		},
	}, nil
}

// approxTokens is a deliberately crude estimate (one token per word). The
// mock only needs usage numbers to be deterministic, not accurate.
func approxTokens(req Request) int {
	n := 0
	for _, msg := range req.Messages {
		n += len(strings.Fields(msg.Content))
	}
	return n
}
