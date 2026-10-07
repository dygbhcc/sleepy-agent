// Package policy holds the rules a script must meet. Code owns the rules, the
// model never does.
//
// Ported from the Sleepy pipeline. Only the word limits are here so far, the
// banned phrases, retry limits and audio tolerances arrive with the QA gates.
package policy

// Policy defines the acceptable size of a script.
type Policy struct {
	MinWords    int // below this the script is too short
	MaxWords    int // above this the script is too long
	TargetWords int // ideal length, used when prompting
}

// ForDuration scales the word limits to an episode length in minutes, using
// the same pacing Sleepy uses: 100 words a minute at the low end, 160 at the
// high end, 130 as the target.
func ForDuration(durationMin int) Policy {
	return Policy{
		MinWords:    durationMin * 100,
		MaxWords:    durationMin * 160,
		TargetWords: durationMin * 130,
	}
}
