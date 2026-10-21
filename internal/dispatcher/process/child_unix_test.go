//go:build unix

package process

import (
	"errors"
	"os"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/meshcloud/meshstack-cli/client/types/xurl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatcher"
	"github.com/meshcloud/building-block-runner/internal/testmeshstack"
)

func Test_RunIgnoringItsStdinClose_IsKilledWithItsProcessGroupAfterTheGrace(t *testing.T) {
	isolate(t)
	server := testmeshstack.NewStatusServer(t)
	cfg := testConfig()
	cfg.Api.Url = xurl.MustParsef("%s", server.URL)
	cfg.Dispatcher.Timeout.Duration = time.Second
	cfg.Dispatcher.KillGrace.Duration = 200 * time.Millisecond
	run := newRun()
	server.Link(&run)

	finished := dispatch(t, cfg, run)

	t.Run("the dispatcher reports the run as failed", func(t *testing.T) {
		assert.Equal(t, dispatcher.Finished{RunUuid: run.Metadata.Uuid, ExitCode: exitUnknown, Failure: "the run exceeded the timeout of 1s"}, finished)
	})

	t.Run("the grandchild is killed too", func(t *testing.T) {
		pidText, err := os.ReadFile(grandchildPidFile())
		require.NoError(t, err)
		pid, err := strconv.Atoi(string(pidText))
		require.NoError(t, err)
		assert.Eventually(t, func() bool {
			return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
		}, 5*time.Second, 50*time.Millisecond, "the grandchild sleep %d still runs", pid)
	})
}

func dispatch(t *testing.T, cfg config.Config, run dispatcher.Run) dispatcher.Finished {
	t.Helper()
	d, err := New(t.Context(), cfg)
	require.NoError(t, err)
	require.NoError(t, d.Dispatch(t.Context(), run, ""))
	select {
	case finished := <-d.Finished():
		return finished
	case <-time.After(30 * time.Second):
		t.Fatal("the run did not finish")
		return dispatcher.Finished{}
	}
}
