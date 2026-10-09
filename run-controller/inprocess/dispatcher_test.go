package inprocess

import (
	"encoding/base64"
	"errors"
	"io"
	"log"
	"testing"
	"time"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
	"github.com/meshcloud/building-block-runner/run-controller/controller"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testDispatcher(execute executeFunc) *Dispatcher {
	return &Dispatcher{
		logger:    log.New(io.Discard, "", 0),
		executors: map[meshapi.RunnerImplementationType]executeFunc{meshapi.RunnerTypeManual: execute},
	}
}

func dispatchManual(t *testing.T, d *Dispatcher, runJson string) {
	t.Helper()
	run := &meshapi.RunDetailsDTO{Metadata: meshapi.RunMetaDTO{Uuid: "run-1"}}
	require.NoError(t, d.Dispatch(run, base64.StdEncoding.EncodeToString([]byte(runJson)), "MANUAL"))
}

func activeRuns(d *Dispatcher) int {
	active, _ := d.CountActive()
	return active
}

func Test_UnsupportedType_ReturnsNoHandlerError(t *testing.T) {
	d := testDispatcher(func([]byte) error { return nil })

	err := d.Dispatch(&meshapi.RunDetailsDTO{}, "", "GITHUB_WORKFLOW")

	noHandler, ok := errors.AsType[*controller.NoHandlerError](err)
	require.True(t, ok, "got %v", err)
	assert.Equal(t, "GITHUB_WORKFLOW", noHandler.RunnerType)
}

func Test_RunningRun_CountsAsActiveUntilItEnds(t *testing.T) {
	received := make(chan []byte, 1)
	release := make(chan struct{})
	d := testDispatcher(func(runJson []byte) error {
		received <- runJson
		<-release
		return nil
	})

	dispatchManual(t, d, `{"run":1}`)

	assert.Equal(t, `{"run":1}`, string(<-received))
	assert.Equal(t, 1, activeRuns(d))
	close(release)
	assert.Eventually(t, func() bool { return activeRuns(d) == 0 }, time.Second, time.Millisecond)
}

func Test_PanickingRun_IsRecoveredAndNoLongerActive(t *testing.T) {
	d := testDispatcher(func([]byte) error { panic("boom") })

	dispatchManual(t, d, `{}`)

	assert.Eventually(t, func() bool { return activeRuns(d) == 0 }, time.Second, time.Millisecond)
}
