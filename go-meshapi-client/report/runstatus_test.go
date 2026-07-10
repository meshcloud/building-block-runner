package report

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunStatus_CurrentStepStatus(t *testing.T) {
	t.Run("in range", func(t *testing.T) {
		r := RunStatus{Steps: []StepStatus{{Name: "a"}, {Name: "b"}}, CurrentStepIndex: 1}
		got := r.CurrentStepStatus()
		require.NotNil(t, got)
		assert.Equal(t, "b", got.Name)
	})

	t.Run("negative index", func(t *testing.T) {
		r := RunStatus{Steps: []StepStatus{{Name: "a"}}, CurrentStepIndex: -1}
		assert.Nil(t, r.CurrentStepStatus())
	})

	t.Run("index past end", func(t *testing.T) {
		r := RunStatus{Steps: []StepStatus{{Name: "a"}}, CurrentStepIndex: 5}
		assert.Nil(t, r.CurrentStepStatus())
	})

	t.Run("mutation through the returned pointer is observed", func(t *testing.T) {
		r := RunStatus{Steps: []StepStatus{{Name: "a", Status: PENDING}}, CurrentStepIndex: 0}
		r.CurrentStepStatus().Status = IN_PROGRESS
		assert.Equal(t, IN_PROGRESS, r.Steps[0].Status)
	})
}

func TestRunStatus_FirstStep(t *testing.T) {
	t.Run("no steps errors", func(t *testing.T) {
		r := RunStatus{CurrentStepIndex: -1}
		err := r.FirstStep()
		require.Error(t, err)
		assert.Equal(t, -1, r.CurrentStepIndex)
	})

	t.Run("positions at index 0", func(t *testing.T) {
		r := RunStatus{Steps: []StepStatus{{Name: "a"}}, CurrentStepIndex: -1}
		require.NoError(t, r.FirstStep())
		assert.Equal(t, 0, r.CurrentStepIndex)
	})
}

func TestRunStatus_NextStep(t *testing.T) {
	t.Run("advances", func(t *testing.T) {
		r := RunStatus{Steps: []StepStatus{{Name: "a"}, {Name: "b"}}, CurrentStepIndex: 0}
		require.NoError(t, r.NextStep())
		assert.Equal(t, 1, r.CurrentStepIndex)
	})

	t.Run("errors at the last step", func(t *testing.T) {
		r := RunStatus{Steps: []StepStatus{{Name: "a"}}, CurrentStepIndex: 0}
		err := r.NextStep()
		require.Error(t, err)
		assert.Equal(t, 0, r.CurrentStepIndex)
	})
}

func TestRunStatus_FailRunAndNotFinishedSteps(t *testing.T) {
	r := RunStatus{
		Status: IN_PROGRESS,
		Steps: []StepStatus{
			{Name: "done", Status: SUCCEEDED},
			{Name: "current", Status: IN_PROGRESS},
			{Name: "later", Status: PENDING},
		},
		CurrentStepIndex: 1,
	}

	r.FailRunAndNotFinishedSteps()

	assert.Equal(t, FAILED, r.Status)
	assert.Equal(t, SUCCEEDED, r.Steps[0].Status, "already-finished steps keep their status")
	assert.Nil(t, r.Steps[0].SystemMessage)

	assert.Equal(t, FAILED, r.Steps[1].Status, "the current step fails")
	assert.Nil(t, r.Steps[1].SystemMessage, "the current step keeps its own message, not the generic one")

	assert.Equal(t, FAILED, r.Steps[2].Status, "later steps fail too")
	require.NotNil(t, r.Steps[2].SystemMessage)
	assert.Equal(t, "Aborted due to failure in an earlier step", *r.Steps[2].SystemMessage)
}

func TestRunStatus_Clone(t *testing.T) {
	summary := "hello"
	original := RunStatus{
		RunId:    "run-1",
		Status:   IN_PROGRESS,
		Steps:    []StepStatus{{Name: "a", Status: PENDING}},
		Summary:  &summary,
		Artifact: []byte{1, 2, 3},
	}

	clone := original.Clone()

	// Independent field values.
	assert.Equal(t, original.RunId, clone.RunId)
	assert.Equal(t, original.Steps, clone.Steps)
	assert.Equal(t, original.Artifact, clone.Artifact)

	// Mutating the clone's slices must not affect the original (B10 fix).
	clone.Steps[0].Status = FAILED
	assert.Equal(t, PENDING, original.Steps[0].Status, "mutating the clone's Steps must not race/alias the original")

	clone.Artifact[0] = 99
	assert.Equal(t, byte(1), original.Artifact[0], "mutating the clone's Artifact must not alias the original")

	// Summary is a pointer field intentionally shared (immutable string content) — Clone does
	// not need to deep-copy it, only Steps/Artifact which are mutated in place elsewhere.
	assert.Same(t, original.Summary, clone.Summary)
}

func TestRunStatus_Clone_NilSlices(t *testing.T) {
	original := RunStatus{RunId: "run-1"}
	clone := original.Clone()
	assert.Nil(t, clone.Steps)
	assert.Nil(t, clone.Artifact)
}
