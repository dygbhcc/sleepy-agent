package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sleepy-agent/internal/errs"
)

func newTestProvider(srv *httptest.Server) *OpenAICompat {
	return NewOpenAICompat("test", srv.URL+"/", "secret-key", "m1")
}

func TestOpenAICompatSendsTheRequestAndParsesTheAnswer(t *testing.T) {
	var gotAuth, gotPath string
	var gotBody chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hello"}}],"usage":{"prompt_tokens":7,"completion_tokens":2}}`))
	}))
	defer srv.Close()

	resp, err := newTestProvider(srv).Complete(context.Background(), Request{
		Messages:    []Message{{Role: RoleSystem, Content: "be brief"}, {Role: RoleUser, Content: "hi"}},
		Temperature: 0.2,
		MaxTokens:   50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hello" || resp.Usage.PromptTokens != 7 || resp.Usage.CompletionTokens != 2 {
		t.Fatalf("unexpected response %+v", resp)
	}
	if gotPath != "/chat/completions" || gotAuth != "Bearer secret-key" {
		t.Fatalf("path %q auth %q", gotPath, gotAuth)
	}
	if gotBody.Model != "m1" || gotBody.Temperature != 0.2 || gotBody.MaxTokens != 50 ||
		len(gotBody.Messages) != 2 || gotBody.Messages[0].Role != "system" {
		t.Fatalf("unexpected request body %+v", gotBody)
	}
}

func TestOpenAICompatRateLimitsAndServerErrorsAreTransient(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"slow down"}}`))
		}))
		_, err := newTestProvider(srv).Complete(context.Background(), Request{})
		srv.Close()

		var te *errs.TransientError
		if !errors.As(err, &te) || te.StatusCode != status {
			t.Fatalf("status %d: want a TransientError carrying it, got %v", status, err)
		}
		if !strings.Contains(err.Error(), "slow down") {
			t.Fatalf("the provider's message should be kept: %v", err)
		}
	}
}

func TestOpenAICompatClientErrorsArePermanent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer srv.Close()

	_, err := newTestProvider(srv).Complete(context.Background(), Request{})
	if err == nil || errs.IsTransient(err) {
		t.Fatalf("a 401 must fail and must not be retried, got %v", err)
	}
}

func TestOpenAICompatNoChoicesIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer srv.Close()

	if _, err := newTestProvider(srv).Complete(context.Background(), Request{}); err == nil {
		t.Fatal("an empty choices list must be an error")
	}
}

func TestOpenAICompatConnectionFailureIsTransientAndHidesTheKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	p := newTestProvider(srv)
	srv.Close() // nothing is listening any more

	_, err := p.Complete(context.Background(), Request{})
	if !errs.IsTransient(err) {
		t.Fatalf("a connection failure should be transient, got %v", err)
	}
	if strings.Contains(err.Error(), "secret-key") {
		t.Fatalf("the API key must never appear in an error: %v", err)
	}
}

func TestOpenAICompatHonoursContextCancellation(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := newTestProvider(srv).Complete(ctx, Request{})
	if !errors.Is(err, context.DeadlineExceeded) || errs.IsTransient(err) {
		t.Fatalf("want a plain deadline error, got %v", err)
	}
}

func TestGroqSetup(t *testing.T) {
	p := NewGroq("k", "some-model")
	if p.baseURL != groqBaseURL || p.model != "some-model" {
		t.Fatalf("unexpected setup: %q %q", p.baseURL, p.model)
	}
	if p.Name() != "groq:some-model" {
		t.Fatalf("name = %q", p.Name())
	}
}

func TestJSONModeSendsResponseFormat(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{}"}}]}`))
	}))
	defer srv.Close()
	p := newTestProvider(srv)
	if _, err := p.Complete(context.Background(), Request{JSON: true}); err != nil {
		t.Fatal(err)
	}
	rf, _ := got["response_format"].(map[string]any)
	if rf["type"] != "json_object" {
		t.Fatalf("response_format = %v", got["response_format"])
	}
	got = nil
	if _, err := p.Complete(context.Background(), Request{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["response_format"]; ok {
		t.Fatal("response_format must be absent when JSON is off")
	}
}
