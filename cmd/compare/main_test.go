package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"sleepy-agent/internal/errs"
	"sleepy-agent/internal/llm"
)

func fast(t *testing.T) {
	t.Helper()
	old := backoff
	backoff = func(int) time.Duration { return time.Millisecond }
	t.Cleanup(func() { backoff = old })
}

func TestRetryingWaitsOutRateLimitsAndCountsThem(t *testing.T) {
	fast(t)
	m := llm.NewMock("ok")
	m.FailNext(errs.NewTransient("groq", 429, errors.New("slow")), errs.NewTransient("groq", 429, errors.New("slow")))
	n := 0
	resp, err := (&retrying{inner: m, count: &n}).Complete(context.Background(), llm.Request{})
	if err != nil || resp.Content != "ok" || n != 2 {
		t.Fatalf("resp %+v err %v retries %d", resp, err, n)
	}
}

func TestRetryingGivesUpAfterMaxRetries(t *testing.T) {
	fast(t)
	m := llm.NewMock()
	for i := 0; i < maxRetries+1; i++ {
		m.FailNext(errs.NewTransient("groq", 429, errors.New("slow")))
	}
	n := 0
	_, err := (&retrying{inner: m, count: &n}).Complete(context.Background(), llm.Request{})
	if !errs.IsTransient(err) || n != maxRetries {
		t.Fatalf("err %v retries %d", err, n)
	}
}

func TestRetryingDoesNotRetryPermanentErrors(t *testing.T) {
	fast(t)
	m := llm.NewMock()
	boom := errors.New("HTTP 400")
	m.FailNext(boom)
	n := 0
	if _, err := (&retrying{inner: m, count: &n}).Complete(context.Background(), llm.Request{}); !errors.Is(err, boom) || n != 0 {
		t.Fatalf("err %v retries %d", err, n)
	}
}
