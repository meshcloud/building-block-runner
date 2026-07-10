package tf

import "errors"

type RunStatus struct {
	RunId            string
	Status           ExecutionStatus
	Steps            []StepStatus
	CurrentStepIndex int
	Summary          *string
	// Artifact holds an optional binary artifact produced by this run.
	// For DETECT runs this contains the binary Terraform plan file so it can be
	// consumed by a subsequent APPLY run.
	Artifact []byte
}

type StepStatus struct {
	Name          string
	DisplayName   string
	Status        ExecutionStatus
	Outputs       map[string]*TfOutput
	UserMessage   *string
	SystemMessage *string
	LogStartIdx   int64
}

func (r *RunStatus) currentStepStatus() *StepStatus {
	if r.CurrentStepIndex >= 0 && r.CurrentStepIndex < len(r.Steps) {
		return &r.Steps[r.CurrentStepIndex]
	} else {
		return nil
	}
}

func (r *RunStatus) firstStep() error {
	if len(r.Steps) == 0 {
		return errors.New("no_steps")
	} else {
		r.CurrentStepIndex = 0
		return nil
	}
}

func (r *RunStatus) nextStep() error {
	if r.CurrentStepIndex+1 < len(r.Steps) {
		r.CurrentStepIndex += 1
		return nil
	} else {
		return errors.New("stepIdx_too_high")
	}
}

func (r *RunStatus) failRunAndNotFinishedSteps() {
	for idx := range r.Steps {
		if idx >= r.CurrentStepIndex {
			r.Steps[idx].Status = FAILED
		}
		if idx > r.CurrentStepIndex {
			r.Steps[idx].SystemMessage = message("Aborted due to failure in an earlier step")
		}
	}
	r.Status = FAILED
}

// clone returns a copy safe to hand to the observer goroutine: the Steps and Artifact slices are
// reallocated so the work goroutine's in-place step-field reassignments cannot race a concurrent
// marshal of the snapshot (B10 fix). StepStatus fields are value/pointer-reassigned (never mutated
// in place), so a value copy of each element is sufficient.
func (r RunStatus) clone() RunStatus {
	c := r
	if r.Steps != nil {
		c.Steps = append([]StepStatus(nil), r.Steps...)
	}
	if r.Artifact != nil {
		c.Artifact = append([]byte(nil), r.Artifact...)
	}
	return c
}
