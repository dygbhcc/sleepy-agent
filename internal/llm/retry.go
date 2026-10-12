package llm

import (
	"context"
	"time"

	"sleepy-agent/internal/errs"
)

// Retrying waits out transient errors (a 429 rate limit is not a model
// failure) and retries the call. A provider that stays unavailable still ends
// as an error, after Max retries.
//
// Long running tools such as cmd/compare and cmd/scriptstats use it. The
// worker has its own retry policy for pipeline steps, so the worker does not.
type Retrying struct {
	Inner Provider
	// Max is the number of retries after the first attempt. Zero means 3.
	Max int
	// Backoff returns the wait before retry number attempt (0 based). Nil means
	// 20s, 40s, 60s, ...
	Backoff func(attempt int) time.Duration
	// OnRetry is called before each wait, for logging and counting.
	OnRetry func(attempt int, wait time.Duration, err error)
}

// Name implements Provider.
func (r *Retrying) Name() string { return r.Inner.Name() }

// Complete implements Provider.
func (r *Retrying) Complete(ctx context.Context, req Request) (Response, error) {
	max := r.Max
	if max <= 0 {
		max = 3
	}
	for attempt := 0; ; attempt++ {
		resp, err := r.Inner.Complete(ctx, req)
		if err == nil || !errs.IsTransient(err) || attempt >= max {
			return resp, err
		}
		wait := time.Duration(20*(attempt+1)) * time.Second
		if r.Backoff != nil {
			wait = r.Backoff(attempt)
		}
		if r.OnRetry != nil {
			r.OnRetry(attempt+1, wait, err)
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return Response{}, ctx.Err()
		}
	}
}
