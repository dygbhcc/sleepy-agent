package domain

import "time"

// Run is one execution of the pipeline for one episode.
//
// It is a trimmed version of Sleepy's Run. Fields for QA, the fix engine and
// the real providers arrive with the days that port that code.
type Run struct {
	ID    string
	Topic string

	// Style is the run's own look and imagery: Cosmos, Earthside or Myth.
	// Empty means the scripter's default. In Sleepy the user sets it when the
	// run is created and the background image follows it.
	Style string

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
	// OutputQAScript is the JSON report of the script gate, kept for passes and
	// for rejections alike.
	OutputQAScript = "qa_script"
	// OutputFixState is the fix engine's memory for the run: the plan applied to
	// the current attempt and the IDs of every plan tried so far (JSON). It is
	// empty when no fix is in play and is cleared when the gate passes.
	OutputFixState = "fix_state"
	// OutputScriptSource says where the script came from: SourceModel or
	// SourceCache. It is kept so a reviewer can tell a cached script from a fresh one.
	OutputScriptSource = "script_source"
	// OutputShadowVerdict is the shadow judge's verdict on the accepted script, as
	// JSON. It is recorded and never acted on.
	OutputShadowVerdict = "shadow_verdict"
)

// Values of OutputScriptSource.
const (
	SourceModel = "model"
	SourceCache = "cache"
)
