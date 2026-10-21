package tf

import (
	"context"
	"errors"
	"io"
	"testing"
	"uuid"

	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/executor"
)

func newAsyncCmd(t *testing.T) (*GenericCmd, *statusRecorder) {
	t.Helper()
	api := &statusRecorder{}
	r := executor.NewReporter(api, uuid.New())
	tfRun := run{behavior: behaviorApply}
	tfRun.Spec.Definition.Spec.Implementation.Terraform = &client.MeshRunnerTerraformImplementation{Async: true}
	cmd := &GenericCmd{ctx: t.Context(), run: tfRun, reporter: r, steps: declareSteps(r, behaviorApply, true)}
	require.NoError(t, r.Register(t.Context()))
	return cmd, api
}

func Test_AsyncRunSucceeded_HandsOverWithOneTriggerStep(t *testing.T) {
	cmd, api := newAsyncCmd(t)
	noop := func() error { return nil }

	require.NoError(t, cmd.runPhases(phase{cmd.steps.sources, noop}, phase{cmd.steps.execute, noop}))
	status, err := cmd.reporter.Finish(t.Context())

	require.NoError(t, err)
	assert.Equal(t, executor.StatusInProgress, status)
	require.Len(t, api.last.Steps, 1)
	assert.Equal(t, StepTrigger, api.last.Steps[0].Id)
	assert.Equal(t, string(executor.StatusSucceeded), *api.last.Steps[0].Status)
}

func Test_AsyncRunFailed_ReportsFailed(t *testing.T) {
	cmd, api := newAsyncCmd(t)

	require.Error(t, cmd.runPhases(phase{cmd.steps.execute, func() error { return errors.New("apply failed") }}))
	status, err := cmd.reporter.Finish(t.Context())

	require.NoError(t, err)
	assert.Equal(t, executor.StatusFailed, status)
	assert.Equal(t, string(executor.StatusFailed), *api.last.Status)
}

type recordingDestroy struct {
	Facade

	calls []string
}

func (f *recordingDestroy) Apply(context.Context, ...tfexec.ApplyOption) error {
	f.calls = append(f.calls, "apply")
	return nil
}

func (f *recordingDestroy) Destroy(context.Context, ...tfexec.DestroyOption) error {
	f.calls = append(f.calls, "destroy")
	return nil
}

func (*recordingDestroy) SetStdout(io.Writer) {}

func (*recordingDestroy) SetStderr(io.Writer) {}

func Test_Destroy_AppliesFirstWhenAsync(t *testing.T) {
	for _, async := range []bool{true, false} {
		cmd, _ := newAsyncCmd(t)
		cmd.run.terraform().Async = async
		tf := &recordingDestroy{}

		require.NoError(t, (&DestroyCommand{GenericCmd: *cmd}).destroy(tf))

		if async {
			assert.Equal(t, []string{"apply", "destroy"}, tf.calls)
		} else {
			assert.Equal(t, []string{"destroy"}, tf.calls)
		}
	}
}
