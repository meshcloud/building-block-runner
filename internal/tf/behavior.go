package tf

import (
	"errors"
)

type Behavior int

const (
	APPLY   = Behavior(iota) // generic term that represents tf apply
	DETECT                   // generic term that represents tf plan (detects state drift)
	DESTROY                  // generic term that represents tf destroy
	UNKNOWN_BEHAVIOR
)

var behaviors = []Behavior{APPLY, DETECT, DESTROY}

// str returns the enum's stable, external-facing name (used both as the DetermineBehavior parse
// target and inside log/status messages). An unmapped value (UNKNOWN_BEHAVIOR, or any value
// outside the declared range) is not a programmer error worth killing the process over: it
// returns "UNKNOWN" so callers formatting a Behavior for logs/messages keep working;
// code needing to reject an unrecognized run-JSON behavior string uses DetermineBehavior's error
// return instead.
func (b Behavior) str() string {
	switch b {
	case APPLY:
		return "APPLY"
	case DETECT:
		return "DETECT"
	case DESTROY:
		return "DESTROY"
	default:
		return "UNKNOWN"
	}
}

func DetermineBehavior(str string) (Behavior, error) {
	for _, b := range behaviors {
		if b.str() == str {
			return b, nil
		}
	}
	return UNKNOWN_BEHAVIOR, errors.New("cannot determine Behavior type")
}
