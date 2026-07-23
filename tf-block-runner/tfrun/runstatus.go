package tfrun

import "errors"

type RunStatus struct {
	RunId            string
	Status           ExecutionStatus
	Steps            []*StepStatus
	CurrentStepIndex int
	Summary          *string
	// ChangesDetected reports whether `terraform plan` found infrastructure changes.
	// Only DETECT runs set it; APPLY/DESTROY leave it nil so it is omitted from the update.
	ChangesDetected *bool
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
		return r.Steps[r.CurrentStepIndex]
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
	for idx, s := range r.Steps {
		if idx >= r.CurrentStepIndex {
			s.Status = FAILED
		}
		if idx > r.CurrentStepIndex {
			s.SystemMessage = message("Aborted due to failure in an earlier step")
		}
	}
	r.Status = FAILED
}
