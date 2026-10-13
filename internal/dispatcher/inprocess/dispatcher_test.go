package inprocess

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatcher"
	"github.com/meshcloud/building-block-runner/internal/http/meshapi"
)

func newRun(implementationType string) dispatcher.Run {
	run := dispatcher.Run{Metadata: meshapi.MeshRunnerRunMetadata{Uuid: uuid.New()}}
	run.Spec.RunToken = "a-run-token"
	run.Spec.Definition.Spec.Implementation = []byte(`{"type":"` + implementationType + `"}`)
	return run
}

type fakeRunnerClient struct {
	meshapi.MeshRunnerClient
}

func (fakeRunnerClient) ForRun(run meshapi.MeshRunnerRun) meshapi.MeshRunnerRunClient {
	return fakeRunClient{run: run}
}

type fakeRunClient struct {
	meshapi.MeshRunnerRunClient

	run meshapi.MeshRunnerRun
}

func manualDispatcher(ctx context.Context, execute executeFunc) *Dispatcher {
	return newDispatcher(ctx, fakeRunnerClient{}, executors{config.RunnerTypeManual: execute}, time.Hour)
}

func Test_Start_FailsForATypeWithoutExecutor(t *testing.T) {
	d := manualDispatcher(t.Context(), func(context.Context, dispatcher.Run, meshapi.MeshRunnerRunClient) error { return nil })

	err := d.Start(t.Context(), newRun("GITHUB_WORKFLOW"))

	assert.EqualError(t, err, "this runner cannot run github runs")
}

func Test_Start_PassesTheRunWithAClientForIt(t *testing.T) {
	type execution struct {
		run dispatcher.Run
		api meshapi.MeshRunnerRunClient
	}
	received := make(chan execution, 1)
	d := manualDispatcher(t.Context(), func(_ context.Context, run dispatcher.Run, api meshapi.MeshRunnerRunClient) error {
		received <- execution{run, api}
		return nil
	})
	run := newRun("MANUAL")

	require.NoError(t, d.Start(t.Context(), run))

	assert.Equal(t, dispatcher.RunStatus{RunUuid: run.Metadata.Uuid, Ended: true}, <-d.Status())
	assert.Equal(t, execution{run, fakeRunClient{run: run}}, <-received)
}

func Test_Run_EndsWithAnErrorWhenItFailsPanicsOrTimesOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		executors := map[string]executeFunc{
			"tofu failed": func(context.Context, dispatcher.Run, meshapi.MeshRunnerRunClient) error {
				return errors.New("tofu failed")
			},
			"the run panicked: boom": func(context.Context, dispatcher.Run, meshapi.MeshRunnerRunClient) error { panic("boom") },
			"the run exceeded the timeout of 1h0m0s": func(ctx context.Context, _ dispatcher.Run, _ meshapi.MeshRunnerRunClient) error {
				<-ctx.Done()
				return nil
			},
		}
		for message, execute := range executors {
			d := manualDispatcher(t.Context(), execute)
			require.NoError(t, d.Start(t.Context(), newRun("MANUAL")))

			status := <-d.Status()

			assert.True(t, status.Ended)
			assert.EqualError(t, status.Err, message)
		}
	})
}

func Test_Status_ClosesAfterTheLastRunEnded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		release := make(chan struct{})
		d := manualDispatcher(ctx, func(ctx context.Context, _ dispatcher.Run, _ meshapi.MeshRunnerRunClient) error {
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		run := newRun("MANUAL")
		require.NoError(t, d.Start(ctx, run))

		cancel()
		synctest.Wait()
		assert.EqualError(t, d.Start(t.Context(), newRun("MANUAL")), "the runner is shutting down")

		close(release)
		assert.Equal(t, dispatcher.RunStatus{RunUuid: run.Metadata.Uuid, Ended: true}, <-d.Status())
		_, open := <-d.Status()
		assert.False(t, open)
	})
}
