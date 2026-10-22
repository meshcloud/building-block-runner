package azuredevops

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/executor"
)

func Test_StageStateAndResult_MapToStepStatusAndMessage(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		state, result string
		wantStatus    executor.Status
		wantMessage   string
	}{
		{"pending", "", executor.StatusPending, "Build is pending"},
		{"inProgress", "", executor.StatusInProgress, "Build is running"},
		{"completed", "succeeded", executor.StatusSucceeded, "Build completed successfully"},
		{"completed", "succeededWithIssues", executor.StatusSucceeded, "Build completed with issues"},
		{"completed", "skipped", executor.StatusSucceeded, "Build was skipped"},
		{"completed", "failed", executor.StatusFailed, "Build failed"},
		{"completed", "abandoned", executor.StatusFailed, "Build was abandoned"},
		{"completed", "canceled", executor.StatusAborted, "Build was canceled"},
		{"completed", "someFutureResult", executor.StatusInProgress, "Build: completed someFutureResult"},
		{"someFutureState", "", executor.StatusInProgress, "Build: someFutureState "},
	} {
		status, message := stageUpdate("Build", c.state, c.result)
		assert.Equal(t, c.wantStatus, status, "%s %s", c.state, c.result)
		assert.Equal(t, c.wantMessage, message, "%s %s", c.state, c.result)
	}
}

func Test_InputValues_BecomeTemplateParameterStrings(t *testing.T) {
	t.Parallel()
	for value, want := range map[any]string{
		nil:                           "",
		"hello":                       "hello",
		true:                          "true",
		big.NewInt(12345678901234567): "12345678901234567",
	} {
		got, err := parameterValue(value)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
	got, err := parameterValue([]any{"a", "b"})
	require.NoError(t, err)
	assert.JSONEq(t, `["a","b"]`, got)
}
