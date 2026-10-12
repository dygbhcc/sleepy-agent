package llm

import (
	"context"
	"errors"
	"testing"
	"time"

	"sleepy-agent/internal/errs"
)

func fastRetry(inner Provider, count *int) *Retrying {
	return &Retrying{
		Inner:   inner,
		Backoff: func(int) time.Duration { return time.Millisecond },
		OnRetry: func(int, time.Duration, error) { *count++ },
	}
}

func rateLimit() error { return errs.NewTransient("groq", 429, errors.New("slow")) }

func TestRetryingWaitsOutRateLimitsAndCountsThem(t *testing.T) {
	m := NewMock("ok")
	m.FailNext(rateLimit(), rateLimit())
	n := 0
	resp, err := fastRetry(m, &n).Complete(context.Background(), Request{})
	if err != nil || resp.Content != "ok" || n != 2 {
		t.Fatalf("resp %+v err %v retries %d", resp, err, n)
	}
}

func TestRetryingGivesUpAfterMax(t *testing.T) {
	m := NewMock()
	for i := 0; i < 4; i++ {
		m.FailNext(rateLimit())
	}
	n := 0
	_, err := fastRetry(m, &n).Complete(context.Background(), Request{})
	if !errs.IsTransient(err) || n != 3 {
		t.Fatalf("err %v retries %d, want a transient error after 3 retries", err, n)
	}
}

func TestRetryingDoesNotRetryPermanentErrors(t *testing.T) {
	m := NewMock()
	boom := errors.New("HTTP 400")
	m.FailNext(boom)
	n := 0
	if _, err := fastRetry(m, &n).Complete(context.Background(), Request{}); !errors.Is(err, boom) || n != 0 {
		t.Fatalf("err %v retries %d", err, n)
	}
}

func TestRetryingStopsWhenTheContextIsCancelled(t *testing.T) {
	m := NewMock()
	m.FailNext(rateLimit())
	ctx, cancel := context.WithCancel(context.Background())
	r := &Retrying{Inner: m, Backoff: func(int) time.Duration { return time.Hour }, OnRetry: func(int, time.Duration, error) { cancel() }}
	if _, err := r.Complete(ctx, Request{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}
