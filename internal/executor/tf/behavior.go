package tf

import (
	"errors"
	"log"
)

type Behavior int

const (
	behaviorApply   = Behavior(iota) // generic term that represents tf apply
	behaviorDetect                   // generic term that represents tf plan (detects state drift)
	behaviorDestroy                  // generic term tat represents tf destroy
	behaviorUnknown
)

var behaviors = []Behavior{behaviorApply, behaviorDetect, behaviorDestroy}

func (b Behavior) str() string {
	switch b {
	case behaviorApply:
		return "APPLY"
	case behaviorDetect:
		return "DETECT"
	case behaviorDestroy:
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
	return behaviorUnknown, errors.New("cannot determine Behavior type")
}
