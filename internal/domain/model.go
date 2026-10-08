package domain

import "time"

// Run is one execution of the pipeline for one episode.
//
// It is a trimmed version of Sleepy's Run. Fields for QA, the fix engine and
// the real providers arrive with the days that port that code.
type Run struct {
	ID    string
	Topic string

	Status RunStatus

	// Outputs holds what each step produced: a title, a script, file references.
	// Keys are the Output* constants.
	Outputs map[string]string

	// FailedAttempts counts consecutive failed attempts of the current step.
	// It resets whenever the status changes.
	FailedAttempts int
	LastError      string

	// Lock, TTL based: a crashed worker's lock expires and the run is reclaimed.
	LockedBy string
	LockedAt time.Time

	// Approval gate. Approved is the human's yes for publishing.
	// AwaitingApproval is set when the Decider answered Ask, so workers leave
	// the run alone until someone approves it.
	Approved         bool
	AwaitingApproval bool

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Locked reports whether the run holds a lock that has not expired at now.
func (r Run) Locked(now time.Time, ttl time.Duration) bool {
	return r.LockedBy != "" && now.Sub(r.LockedAt) < ttl
}

// Keys of Run.Outputs.
const (
	OutputTitle     = "title"
	OutputScript    = "script"
	OutputVoice     = "voice"
	OutputThumbnail = "thumbnail"
	OutputVideo     = "video"
	OutputPackage   = "package"
	OutputPublished = "published"
)
