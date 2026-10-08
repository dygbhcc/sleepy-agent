package domain_test

import (
	"testing"
	"time"

	"sleepy-agent/internal/domain"
)

func TestNextStatusWalksTheWholeChain(t *testing.T) {
	want := []domain.RunStatus{
		domain.StatusPending, domain.StatusScripted, domain.StatusVoiced,
		domain.StatusThumbnailed, domain.StatusRendered, domain.StatusPackaged,
		domain.StatusUploaded, domain.StatusDone,
	}
	for i := 0; i < len(want)-1; i++ {
		if got := domain.NextStatus(want[i]); got != want[i+1] {
			t.Errorf("NextStatus(%s) = %s, want %s", want[i], got, want[i+1])
		}
	}
}

func TestNextStatusNeverAdvancesTerminalOrUnknown(t *testing.T) {
	for _, s := range []domain.RunStatus{
		domain.StatusDone, domain.StatusFailed, domain.StatusNeedsReview, "SOMETHING_ELSE",
	} {
		if got := domain.NextStatus(s); got != domain.StatusFailed {
			t.Errorf("NextStatus(%s) = %s, want FAILED", s, got)
		}
	}
}

func TestTerminal(t *testing.T) {
	for _, s := range []domain.RunStatus{domain.StatusDone, domain.StatusFailed, domain.StatusNeedsReview} {
		if !s.Terminal() {
			t.Errorf("%s should be terminal", s)
		}
	}
	for _, s := range []domain.RunStatus{domain.StatusPending, domain.StatusPackaged, domain.StatusUploaded} {
		if s.Terminal() {
			t.Errorf("%s should not be terminal", s)
		}
	}
}

func TestLockedHonoursTTL(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	r := domain.Run{LockedBy: "w1", LockedAt: now}
	if !r.Locked(now.Add(4*time.Minute), 5*time.Minute) {
		t.Error("lock should still hold inside the TTL")
	}
	if r.Locked(now.Add(5*time.Minute), 5*time.Minute) {
		t.Error("lock should expire at the TTL")
	}
	if (domain.Run{}).Locked(now, time.Minute) {
		t.Error("an unlocked run is not locked")
	}
}
