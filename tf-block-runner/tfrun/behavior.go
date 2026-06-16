package tfrun

import (
	"errors"
	"log"
)

type Behavior int

const (
	APPLY   = Behavior(iota) // generic term that represents tf apply
	DETECT                   // generic term that represents tf plan (detects state drift)
	DESTROY                  // generic term tat represents tf destory
	UNKNOWN_BEHAVIOR
)

var behaviors = []Behavior{APPLY, DETECT, DESTROY}

func (b Behavior) str() string {
	switch b {
	case APPLY:
		return "APPLY"
	case DETECT:
		return "DETECT"
	case DESTROY:
		return "DESTROY"
	default:
		log.Fatalf("Behavior.str() not implemented for %d", b)
		return "" // never reached
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
