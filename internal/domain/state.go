// Package domain holds the pipeline state machine.
//
// Ported from the Sleepy pipeline (internal/domain/state.go). The status chain
// is kept exactly, so data and tooling from Sleepy keep their meaning.
package domain

// RunStatus is the pipeline state of a run.
//
// A status names the last step that finished, not the step in progress. The
// step that runs while a run sits in a status is chosen by that status, see
// runner.stageFor. Note the one oddity inherited from Sleepy: UPLOADED means
// "packaged and queued for upload". The upload itself runs while the run is
// in UPLOADED, and only then does the run become DONE.
type RunStatus string

const (
	StatusPending     RunStatus = "PENDING"
	StatusScripted    RunStatus = "SCRIPTED"
	StatusVoiced      RunStatus = "VOICED"
	StatusThumbnailed RunStatus = "THUMBNAILED"
	StatusRendered    RunStatus = "RENDERED"
	StatusPackaged    RunStatus = "PACKAGED"
	StatusUploaded    RunStatus = "UPLOADED"
	StatusDone        RunStatus = "DONE"
	StatusFailed      RunStatus = "FAILED"
	StatusNeedsReview RunStatus = "NEEDS_REVIEW"
)

// Terminal reports whether no worker should touch a run in this status.
func (s RunStatus) Terminal() bool {
	return s == StatusDone || s == StatusFailed || s == StatusNeedsReview
}

// NextStatus returns the status a run advances to after its current step
// succeeds. Anything that has no successor, including unknown statuses, maps
// to FAILED so a bug cannot silently advance a run.
func NextStatus(s RunStatus) RunStatus {
	switch s {
	case StatusPending:
		return StatusScripted
	case StatusScripted:
		return StatusVoiced
	case StatusVoiced:
		return StatusThumbnailed
	case StatusThumbnailed:
		return StatusRendered
	case StatusRendered:
		return StatusPackaged
	case StatusPackaged:
		return StatusUploaded
	case StatusUploaded:
		return StatusDone
	default:
		return StatusFailed
	}
}
