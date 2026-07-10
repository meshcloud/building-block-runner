package report

import "errors"

// Output mirrors meshapi.OutputDTO but stays free of the meshapi import at the domain type
// level (P3: the domain shape must not depend on the wire DTO it is later mapped to).
type Output struct {
	Value     any
	Type      string
	Sensitive bool
}

// StepStatus is one step of a run's reported progress.
type StepStatus struct {
	Name          string
	DisplayName   string
	Status        ExecutionStatus
	Outputs       map[string]Output
	UserMessage   *string
	SystemMessage *string
	LogStartIdx   int64
}

// RunStatus is the runner-agnostic, in-memory view of a run's reported progress: the shape
// every runner's Reporter (tf and the four phase-6 ports) is built around. Steps is a value
// slice (B10/B6 fix carried forward, plan 02 §5.5): the work goroutine only ever replaces it
// wholesale via Clone()/mutation helpers below, never mutates a shared backing array in place,
// so a Snapshot handed to an observer goroutine can never race a concurrent write.
type RunStatus struct {
	RunId            string
	Status           ExecutionStatus
	Steps            []StepStatus
	CurrentStepIndex int
	Summary          *string
	// Artifact holds an optional binary artifact produced by this run. For DETECT runs this
	// contains the binary Terraform plan file so it can be consumed by a subsequent APPLY run.
	Artifact []byte
}

// CurrentStepStatus returns a pointer to the step at CurrentStepIndex, or nil when the index
// is out of range (including the "no steps registered yet" -1 sentinel).
func (r *RunStatus) CurrentStepStatus() *StepStatus {
	if r.CurrentStepIndex >= 0 && r.CurrentStepIndex < len(r.Steps) {
		return &r.Steps[r.CurrentStepIndex]
	}
	return nil
}

// FirstStep positions CurrentStepIndex at the first step; it fails when no steps are registered.
func (r *RunStatus) FirstStep() error {
	if len(r.Steps) == 0 {
		return errors.New("no_steps")
	}
	r.CurrentStepIndex = 0
	return nil
}

// NextStep advances CurrentStepIndex by one; it fails when already at the last step.
func (r *RunStatus) NextStep() error {
	if r.CurrentStepIndex+1 < len(r.Steps) {
		r.CurrentStepIndex++
		return nil
	}
	return errors.New("stepIdx_too_high")
}

// FailRunAndNotFinishedSteps marks the run FAILED, fails the current and every later step, and
// stamps every step after the current one with an explanatory SystemMessage (the current step
// keeps whatever message it already carries — it is the one that actually failed).
func (r *RunStatus) FailRunAndNotFinishedSteps() {
	for idx := range r.Steps {
		if idx >= r.CurrentStepIndex {
			r.Steps[idx].Status = FAILED
		}
		if idx > r.CurrentStepIndex {
			r.Steps[idx].SystemMessage = new("Aborted due to failure in an earlier step")
		}
	}
	r.Status = FAILED
}

// Clone returns a copy safe to hand to another goroutine: Steps and Artifact are reallocated so
// in-place step-field reassignments on the original cannot race a concurrent read of the clone
// (B10 fix). StepStatus fields are value/pointer-reassigned, never mutated in place, so a value
// copy of each element is sufficient — no deep-copy of Outputs/UserMessage/SystemMessage needed.
func (r RunStatus) Clone() RunStatus {
	c := r
	if r.Steps != nil {
		c.Steps = append([]StepStatus(nil), r.Steps...)
	}
	if r.Artifact != nil {
		c.Artifact = append([]byte(nil), r.Artifact...)
	}
	return c
}
