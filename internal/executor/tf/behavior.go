package tf

import (
	"errors"
	"slices"
)

type Behavior string

const (
	behaviorApply   Behavior = "APPLY"
	behaviorDetect  Behavior = "DETECT"
	behaviorDestroy Behavior = "DESTROY"
)

func DetermineBehavior(name string) (Behavior, error) {
	if b := Behavior(name); slices.Contains([]Behavior{behaviorApply, behaviorDetect, behaviorDestroy}, b) {
		return b, nil
	}
	return "", errors.New("cannot determine Behavior type")
}
