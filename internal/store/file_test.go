package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"sleepy-agent/internal/domain"
	"sleepy-agent/internal/store"
)

func TestClaimIsOldestFirstAndSkipsTerminalAndWaitingRuns(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory(nil)
	a, _ := s.CreateRun(ctx, "a")
	b, _ := s.CreateRun(ctx, "b")
	c, _ := s.CreateRun(ctx, "c")
	if err := s.SetFailed(ctx, a.ID, "x"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAwaitingApproval(ctx, b.ID, true); err != nil {
		t.Fatal(err)
	}

	got, err := s.ClaimNextRun(ctx, "w")
	if err != nil || got == nil || got.ID != c.ID {
		t.Fatalf("want %s, got %+v %v", c.ID, got, err)
	}
	if next, _ := s.ClaimNextRun(ctx, "w"); next != nil {
		t.Fatalf("nothing else should be eligible, got %s", next.ID)
	}
	if err := s.ApprovePublish(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if next, _ := s.ClaimNextRun(ctx, "w"); next == nil || next.ID != b.ID {
		t.Fatal("approving should make the run eligible again")
	}
}

func TestFilePersistsEveryChange(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runs.json")
	s, err := store.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := s.CreateRun(ctx, "forest")
	_ = s.SetOutput(ctx, r.ID, domain.OutputTitle, "Quiet")
	_ = s.UpdateRunStatus(ctx, r.ID, domain.StatusScripted)

	again, err := store.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := again.GetRun(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Topic != "forest" || got.Status != domain.StatusScripted || got.Outputs[domain.OutputTitle] != "Quiet" {
		t.Fatalf("state was not persisted: %+v", got)
	}
	next, _ := again.CreateRun(ctx, "second")
	if next.ID == r.ID {
		t.Fatalf("IDs must not repeat after a reload, both are %s", r.ID)
	}
}

func TestGetReturnsACopy(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory(nil)
	r, _ := s.CreateRun(ctx, "t")
	_ = s.SetOutput(ctx, r.ID, "k", "v")
	got, _ := s.GetRun(ctx, r.ID)
	got.Outputs["k"] = "changed"
	got.Status = domain.StatusDone
	again, _ := s.GetRun(ctx, r.ID)
	if again.Outputs["k"] != "v" || again.Status != domain.StatusPending {
		t.Fatalf("caller mutated the store through a returned run: %+v", again)
	}
}

func TestUnknownRun(t *testing.T) {
	s := store.NewMemory(nil)
	if _, err := s.GetRun(context.Background(), "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if err := s.SetOutput(context.Background(), "nope", "k", "v"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}
