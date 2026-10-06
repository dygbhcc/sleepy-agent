package llm

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestMockAnswersInOrder(t *testing.T) {
	m := NewMock("first", "second")
	ctx := context.Background()

	for _, want := range []string{"first", "second"} {
		got, err := m.Complete(ctx, Request{Messages: []Message{{Role: RoleUser, Content: "hi"}}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Content != want {
			t.Fatalf("got %q, want %q", got.Content, want)
		}
	}
}

func TestMockScriptExhausted(t *testing.T) {
	m := NewMock("only")
	ctx := context.Background()
	if _, err := m.Complete(ctx, Request{}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Complete(ctx, Request{}); !errors.Is(err, ErrScriptExhausted) {
		t.Fatalf("got %v, want ErrScriptExhausted", err)
	}
}

func TestMockFailNextThenRecovers(t *testing.T) {
	boom := errors.New("boom")
	m := NewMock("ok")
	m.FailNext(boom)
	ctx := context.Background()

	if _, err := m.Complete(ctx, Request{}); !errors.Is(err, boom) {
		t.Fatalf("got %v, want boom", err)
	}
	got, err := m.Complete(ctx, Request{})
	if err != nil || got.Content != "ok" {
		t.Fatalf("got (%q, %v), want (ok, nil)", got.Content, err)
	}
}

func TestMockHonoursCancelledContext(t *testing.T) {
	m := NewMock("never")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Complete(ctx, Request{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	if len(m.Requests()) != 0 {
		t.Fatal("a cancelled call must not be recorded")
	}
}

func TestMockRecordsRequests(t *testing.T) {
	m := NewMock("a")
	req := Request{Messages: []Message{{Role: RoleUser, Content: "write a script"}}}
	if _, err := m.Complete(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	got := m.Requests()
	if len(got) != 1 || got[0].Messages[0].Content != "write a script" {
		t.Fatalf("unexpected recorded requests: %+v", got)
	}
}

func TestMockIsSafeForConcurrentUse(t *testing.T) {
	const n = 50
	script := make([]string, n)
	for i := range script {
		script[i] = "x"
	}
	m := NewMock(script...)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := m.Complete(context.Background(), Request{}); err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := len(m.Requests()); got != n {
		t.Fatalf("recorded %d requests, want %d", got, n)
	}
}
