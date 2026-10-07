package errs

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestIsTransient(t *testing.T) {
	cause := errors.New("rate limited")
	err := NewTransient("groq", 429, cause)

	if !IsTransient(err) {
		t.Fatal("a TransientError must be transient")
	}
	if !IsTransient(fmt.Errorf("step failed: %w", err)) {
		t.Fatal("a wrapped TransientError must still be transient")
	}
	if IsTransient(errors.New("plain")) {
		t.Fatal("a plain error must not be transient")
	}
	if IsTransient(nil) {
		t.Fatal("nil must not be transient")
	}
}

func TestTransientUnwrapAndMessage(t *testing.T) {
	cause := errors.New("boom")
	err := NewTransient("groq", 503, cause)

	if !errors.Is(err, cause) {
		t.Fatal("errors.Is must reach the cause")
	}
	if !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("message should include the status, got %q", err.Error())
	}
	if msg := NewTransient("ffmpeg", 0, cause).Error(); strings.Contains(msg, "HTTP") {
		t.Fatalf("non HTTP failure should not mention HTTP, got %q", msg)
	}
}
