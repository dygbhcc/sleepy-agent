package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
)

// Level is how much the pipeline may do without a human. A level is asked for
// in the policy file, and code decides whether the evidence allows it.
type Level int

const (
	// LevelHalt is the kill switch: every step is stopped. It is also what an
	// unreadable or invalid policy means.
	LevelHalt Level = 0
	// LevelReview is the default: every step runs, publishing waits for a
	// human's yes.
	LevelReview Level = 1
	// LevelUnlisted lets an unlisted publish go without a human's yes. It needs
	// evidence, see Autonomy.Granted. Public publishing is not a level: no
	// setting here allows it.
	LevelUnlisted Level = 2
)

func (l Level) String() string {
	switch l {
	case LevelHalt:
		return "halt"
	case LevelReview:
		return "review"
	case LevelUnlisted:
		return "unlisted"
	default:
		return fmt.Sprintf("unknown(%d)", int(l))
	}
}

// What the evidence has to show before LevelUnlisted is granted. They are
// constants in code on purpose: a policy file may ask for a level but cannot
// lower the bar.
const (
	// MinBadScripts is the fewest known-bad scripts a measurement needs.
	MinBadScripts = 30
	// MaxLetThroughUpper is the highest allowed upper end of the 95 percent
	// Wilson interval for "a bad script passed the checks".
	MaxLetThroughUpper = 0.10
)

// Evidence is the latest measurement of the quality checks against labels, as
// counts. The bound is computed here from the counts, never read from a file.
type Evidence struct {
	// OwnerLabelled is true when the owner, not a model, wrote the labels.
	OwnerLabelled bool `json:"owner_labelled"`
	// BadScripts is how many scripts labelled bad the checks saw.
	BadScripts int `json:"bad_scripts"`
	// BadLetThrough is how many of those the checks passed.
	BadLetThrough int `json:"bad_let_through"`
	// Source and MeasuredOn say where the numbers come from. They are for
	// people, code does not read them.
	Source     string `json:"source"`
	MeasuredOn string `json:"measured_on"`
}

// Autonomy is the policy file: the level asked for and the evidence behind it.
type Autonomy struct {
	Level    Level    `json:"level"`
	Evidence Evidence `json:"evidence"`
}

// ParseAutonomy reads a policy file. Unknown fields and levels are errors, so
// a typo cannot quietly turn into a permission.
func ParseAutonomy(data []byte) (Autonomy, error) {
	var a Autonomy
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return Autonomy{}, fmt.Errorf("autonomy policy: %w", err)
	}
	if dec.More() {
		return Autonomy{}, errors.New("autonomy policy: more than one JSON value")
	}
	if a.Level < LevelHalt || a.Level > LevelUnlisted {
		return Autonomy{}, fmt.Errorf("autonomy policy: unknown level %d", int(a.Level))
	}
	if e := a.Evidence; e.BadScripts < 0 || e.BadLetThrough < 0 || e.BadLetThrough > e.BadScripts {
		return Autonomy{}, fmt.Errorf("autonomy policy: evidence counts do not add up (%d of %d)", e.BadLetThrough, e.BadScripts)
	}
	return a, nil
}

// Granted is the level the code allows. It is never above the level asked for.
// The reason is empty when the level asked for is granted.
func (a Autonomy) Granted() (Level, string) {
	switch a.Level {
	case LevelHalt:
		return LevelHalt, "policy level is halt"
	case LevelReview:
		return LevelReview, ""
	case LevelUnlisted:
		if missing := a.Evidence.shortfalls(); len(missing) > 0 {
			return LevelReview, "unlisted level asked for, evidence is not enough: " + strings.Join(missing, "; ")
		}
		return LevelUnlisted, ""
	default:
		// Not reachable through ParseAutonomy; a hand built value fails closed.
		return LevelHalt, fmt.Sprintf("unknown level %d", int(a.Level))
	}
}

func (e Evidence) shortfalls() []string {
	var out []string
	if !e.OwnerLabelled {
		out = append(out, "labels are not the owner's")
	}
	if e.BadScripts < MinBadScripts {
		out = append(out, fmt.Sprintf("%d bad scripts measured, %d needed", e.BadScripts, MinBadScripts))
	}
	if up := WilsonUpper(e.BadLetThrough, e.BadScripts); up > MaxLetThroughUpper {
		out = append(out, fmt.Sprintf("%d of %d bad scripts passed the checks (upper bound %.0f percent, at most %.0f percent allowed)",
			e.BadLetThrough, e.BadScripts, up*100, MaxLetThroughUpper*100))
	}
	return out
}

// WilsonUpper is the upper end of the 95 percent Wilson interval for k events
// in n tries. With no tries it is 1: nothing is known.
func WilsonUpper(k, n int) float64 {
	if n <= 0 {
		return 1
	}
	const z = 1.96
	p, nn := float64(k)/float64(n), float64(n)
	centre := p + z*z/(2*nn)
	margin := z * math.Sqrt(p*(1-p)/nn+z*z/(4*nn*nn))
	return (centre + margin) / (1 + z*z/nn)
}
