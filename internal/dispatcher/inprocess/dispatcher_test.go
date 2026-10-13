package inprocess

import (
	"context"
	"encoding/json/v2"
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

func manualDispatcher(ctx context.Context, execute executeFunc) *Dispatcher {
	return newDispatcher(ctx, executors{config.RunnerTypeManual: execute}, time.Hour)
}

func Test_Start_FailsForATypeWithoutExecutor(t *testing.T) {
	d := manualDispatcher(t.Context(), func(context.Context, []byte) error { return nil })

	err := d.Start(t.Context(), newRun("GITHUB_WORKFLOW"))

	assert.EqualError(t, err, "this runner cannot run github runs")
}

func Test_Start_PassesTheRunAsJsonWithItsToken(t *testing.T) {
	received := make(chan []byte, 1)
	d := manualDispatcher(t.Context(), func(_ context.Context, runJson []byte) error {
		received <- runJson
		return nil
	})
	run := newRun("MANUAL")

	require.NoError(t, d.Start(t.Context(), run))

	assert.Equal(t, dispatcher.RunStatus{RunUuid: run.Metadata.Uuid, Ended: true}, <-d.Status())
	var decoded dispatcher.Run
	require.NoError(t, json.Unmarshal(<-received, &decoded))
	assert.Equal(t, "a-run-token", decoded.Spec.RunToken)
}

func Test_Run_EndsWithAnErrorWhenItFailsPanicsOrTimesOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		executors := map[string]executeFunc{
			"tofu failed":                            func(context.Context, []byte) error { return errors.New("tofu failed") },
			"the run panicked: boom":                 func(context.Context, []byte) error { panic("boom") },
			"the run exceeded the timeout of 1h0m0s": func(ctx context.Context, _ []byte) error { <-ctx.Done(); return nil },
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
		d := manualDispatcher(ctx, func(ctx context.Context, _ []byte) error {
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
