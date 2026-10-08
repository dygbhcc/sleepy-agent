// Package store keeps run state. Sleepy keeps it in Postgres; here the same
// operations sit behind runner.Store, and the first implementation is a plain
// in-memory map that can also persist itself to a JSON file.
//
// That is enough for one process and for the resume test: a "crash" is a new
// File opened on the same path. Postgres can come back later behind the same
// interface if more than one worker process is ever needed.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"sleepy-agent/internal/domain"
)

// DefaultLockTTL is how long a worker may hold a run before others reclaim it.
// It is the value Sleepy uses.
const DefaultLockTTL = 5 * time.Minute

// ErrNotFound is returned for an unknown run ID.
var ErrNotFound = errors.New("store: run not found")

// File is a Store backed by memory, optionally flushed to a JSON file.
// It is safe for concurrent use.
type File struct {
	// LockTTL can be changed before the store is shared. Zero means DefaultLockTTL.
	LockTTL time.Duration

	mu    sync.Mutex
	path  string // empty = memory only
	clock func() time.Time
	runs  []*domain.Run // creation order
}

// NewMemory returns a store that lives only in memory. A nil clock means time.Now.
func NewMemory(clock func() time.Time) *File {
	if clock == nil {
		clock = time.Now
	}
	return &File{clock: clock}
}

// Open loads the store from path, or starts empty if the file does not exist.
// Every later change is written back to path. A nil clock means time.Now.
func Open(path string, clock func() time.Time) (*File, error) {
	s := NewMemory(clock)
	s.path = path
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: read %s: %w", path, err)
	}
	var runs []*domain.Run
	if err := json.Unmarshal(data, &runs); err != nil {
		return nil, fmt.Errorf("store: parse %s: %w", path, err)
	}
	s.runs = runs
	return s, nil
}

func (s *File) ttl() time.Duration {
	if s.LockTTL > 0 {
		return s.LockTTL
	}
	return DefaultLockTTL
}

// CreateRun adds a run in PENDING.
func (s *File) CreateRun(_ context.Context, topic string) (domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	r := &domain.Run{
		ID:        fmt.Sprintf("run-%d", len(s.runs)+1),
		Topic:     topic,
		Status:    domain.StatusPending,
		Outputs:   map[string]string{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.runs = append(s.runs, r)
	if err := s.flush(); err != nil {
		s.runs = s.runs[:len(s.runs)-1]
		return domain.Run{}, err
	}
	return clone(r), nil
}

// GetRun returns a copy of the run.
func (s *File) GetRun(_ context.Context, id string) (domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.find(id)
	if err != nil {
		return domain.Run{}, err
	}
	return clone(r), nil
}

// ClaimNextRun atomically locks the oldest eligible run for workerID.
//
// A run is eligible when it is not terminal, is not waiting for approval, and
// is unlocked or its lock has expired. It returns nil, nil if nothing is
// eligible.
func (s *File) ClaimNextRun(_ context.Context, workerID string) (*domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	for _, r := range s.runs {
		if r.Status.Terminal() || r.AwaitingApproval || r.Locked(now, s.ttl()) {
			continue
		}
		prev := *r
		r.LockedBy, r.LockedAt, r.UpdatedAt = workerID, now, now
		if err := s.flush(); err != nil {
			*r = prev
			return nil, err
		}
		c := clone(r)
		return &c, nil
	}
	return nil, nil
}

// ReleaseRun drops the lock on a run.
func (s *File) ReleaseRun(_ context.Context, id string) error {
	return s.mutate(id, func(r *domain.Run, _ time.Time) {
		r.LockedBy, r.LockedAt = "", time.Time{}
	})
}

// UpdateRunStatus moves a run to status and resets its failure counter.
func (s *File) UpdateRunStatus(_ context.Context, id string, status domain.RunStatus) error {
	return s.mutate(id, func(r *domain.Run, _ time.Time) {
		r.Status = status
		r.FailedAttempts = 0
		r.LastError = ""
	})
}

// RecordFailure counts one failed attempt of the current step and returns the
// new total.
func (s *File) RecordFailure(_ context.Context, id, msg string) (int, error) {
	var n int
	err := s.mutate(id, func(r *domain.Run, _ time.Time) {
		r.FailedAttempts++
		r.LastError = msg
		n = r.FailedAttempts
	})
	return n, err
}

// SetNeedsReview parks a run for a human, with the reason.
func (s *File) SetNeedsReview(_ context.Context, id, reason string) error {
	return s.mutate(id, func(r *domain.Run, _ time.Time) {
		r.Status = domain.StatusNeedsReview
		r.LastError = reason
	})
}

// SetFailed ends a run that cannot succeed, with the reason.
func (s *File) SetFailed(_ context.Context, id, reason string) error {
	return s.mutate(id, func(r *domain.Run, _ time.Time) {
		r.Status = domain.StatusFailed
		r.LastError = reason
	})
}

// SetOutput records what a step produced.
func (s *File) SetOutput(_ context.Context, id, key, value string) error {
	return s.mutate(id, func(r *domain.Run, _ time.Time) {
		if r.Outputs == nil {
			r.Outputs = map[string]string{}
		}
		r.Outputs[key] = value
	})
}

// SetAwaitingApproval marks whether workers must leave the run alone until a
// human approves it.
func (s *File) SetAwaitingApproval(_ context.Context, id string, waiting bool) error {
	return s.mutate(id, func(r *domain.Run, _ time.Time) { r.AwaitingApproval = waiting })
}

// ApprovePublish records the human's yes and lets workers pick the run up again.
func (s *File) ApprovePublish(_ context.Context, id string) error {
	return s.mutate(id, func(r *domain.Run, _ time.Time) {
		r.Approved = true
		r.AwaitingApproval = false
	})
}

func (s *File) mutate(id string, fn func(*domain.Run, time.Time)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.find(id)
	if err != nil {
		return err
	}
	prev := clone(r)
	fn(r, s.clock())
	r.UpdatedAt = s.clock()
	if err := s.flush(); err != nil {
		*r = prev
		return err
	}
	return nil
}

func (s *File) find(id string) (*domain.Run, error) {
	for _, r := range s.runs {
		if r.ID == id {
			return r, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
}

// flush writes the whole state to disk through a temp file and a rename, so a
// crash never leaves a half written file. Callers hold s.mu.
func (s *File) flush() error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(s.runs, "", "  ")
	if err != nil {
		return fmt.Errorf("store: encode: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".runs-*.tmp")
	if err != nil {
		return fmt.Errorf("store: temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return fmt.Errorf("store: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("store: close: %w", err)
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("store: rename: %w", err)
	}
	return nil
}

func clone(r *domain.Run) domain.Run {
	c := *r
	c.Outputs = make(map[string]string, len(r.Outputs))
	for k, v := range r.Outputs {
		c.Outputs[k] = v
	}
	return c
}
