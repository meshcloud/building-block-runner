package runnerproc

import (
	"context"
	"encoding/json/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const stopTimeout = 3 * time.Minute

type Process struct {
	Uuid    string
	Records *Log

	cmd    *exec.Cmd
	exited chan struct{}
}

// Start stops the runner when the test ends, so that cleanups registered later run while it still claims.
func Start(t *testing.T, c Config) Process {
	t.Helper()
	configFile := filepath.Join(c.Dir, "runner-config.yml")
	// A JSON document is YAML too.
	runnerConfig, err := json.Marshal(c.runnerConfig())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configFile, runnerConfig, 0o600))

	home, tmp := filepath.Join(c.Dir, "home"), filepath.Join(c.Dir, "tmp")
	require.NoError(t, os.Mkdir(home, 0o700))
	require.NoError(t, os.Mkdir(tmp, 0o700))
	cmd := exec.CommandContext(context.WithoutCancel(t.Context()), c.Binary)
	cmd.Dir = c.Dir
	cmd.Env = []string{
		"RUNNER_CONFIG_FILE=" + configFile,
		"RUNNER_LOG_FORMAT=json",
		"RUNNER_LOG_LEVEL=debug",
		"GOCOVERDIR=" + c.CoverDir,
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"TMPDIR=" + tmp,
		"MESHSTACK_SKIP_VERSION_CHECK=1",
		"NIX_CONFIG=experimental-features = nix-command flakes",
	}
	if c.MetricsPort != 0 {
		cmd.Env = append(cmd.Env, "RUNNER_METRICS_PORT="+strconv.Itoa(c.MetricsPort))
	}
	if c.ApiUrl != "" {
		cmd.Env = append(cmd.Env, "RUNNER_API_URL="+c.ApiUrl)
	}
	if c.SshKnownHosts != "" {
		cmd.Env = append(cmd.Env, "SSH_KNOWN_HOSTS="+c.SshKnownHosts)
	}
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	cmd.Stderr = cmd.Stdout
	require.NoError(t, cmd.Start())

	p := Process{Uuid: c.Uuid, Records: &Log{}, cmd: cmd, exited: make(chan struct{})}
	go func() {
		defer close(p.exited)
		p.Records.read(stdout)
		_ = cmd.Wait()
	}()
	t.Cleanup(func() {
		p.stop(t)
		if t.Failed() {
			t.Logf("log of runner %s:\n%s", p.Uuid, p.Records.raw())
		}
	})
	return p
}

func (p Process) stop(t *testing.T) {
	t.Helper()
	require.NoError(t, p.cmd.Process.Signal(syscall.SIGTERM))
	select {
	case <-p.exited:
		assert.Equal(t, 0, p.cmd.ProcessState.ExitCode(), "the runner exits 0 after SIGTERM once its runs are done")
	case <-time.After(stopTimeout):
		assert.Fail(t, "the runner did not stop after SIGTERM", "waited %s", stopTimeout)
		_ = p.cmd.Process.Kill()
	}
}
