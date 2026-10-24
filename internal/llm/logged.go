package llm

import (
	"context"
	"fmt"
	"io"
	"sync"
)

// Logged wraps a Provider and writes every call to W: what was sent and what
// came back. It is for experiments, so a finding about a script can be traced
// to the exact prompt behind it. It changes nothing about the call.
type Logged struct {
	Inner Provider
	W     io.Writer

	mu sync.Mutex
	n  int
}

// Name implements Provider.
func (l *Logged) Name() string { return l.Inner.Name() }

// Complete implements Provider.
func (l *Logged) Complete(ctx context.Context, req Request) (Response, error) {
	resp, err := l.Inner.Complete(ctx, req)

	l.mu.Lock()
	defer l.mu.Unlock()
	l.n++
	fmt.Fprintf(l.W, "######## call %d: temperature %.2f, max_tokens %d\n", l.n, req.Temperature, req.MaxTokens)
	for _, m := range req.Messages {
		fmt.Fprintf(l.W, "\n---- %s\n%s\n", m.Role, m.Content)
	}
	if err != nil {
		fmt.Fprintf(l.W, "\n---- error\n%v\n\n", err)
		return resp, err
	}
	fmt.Fprintf(l.W, "\n---- reply (finish_reason %q, %d prompt tokens, %d completion tokens of which %d reasoning)\n%s\n\n",
		resp.FinishReason, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, resp.ReasoningTokens, resp.Content)
	return resp, nil
}
