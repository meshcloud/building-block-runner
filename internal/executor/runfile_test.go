package executor

import (
	"context"
	"encoding/json/v2"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"uuid"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/pkg/setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/config/tf"
	"github.com/meshcloud/building-block-runner/internal/config/xtime"
	"github.com/meshcloud/building-block-runner/internal/testmeshstack"
)

var runnerUuid = uuid.MustParse("98520496-627d-43e6-82da-ce499179ff3f")

func TestMain(m *testing.M) {
	// go test may give the binary a pipe as stdin, which RunFile would take for the lifeline of a run process.
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		panic(err)
	}
	os.Stdin = devNull
	Register(config.RunnerTypeManual, func(struct{}) (Executor, error) { return fakeExecute, nil })
	os.Exit(m.Run())
}

func fakeExecute(ctx context.Context, run Run) error {
	step := run.Reporter.Step("fake", "Fake")
	if err := run.Reporter.Register(ctx); err != nil {
		return err
	}
	return step.Run(func() error {
		if run.Spec.Behavior == "fail" {
			return errors.New("the fake run failed")
		}
		return nil
	})
}

func manualRun(behavior string) client.MeshRunnerRun {
	run := client.MeshRunnerRun{Metadata: client.MeshRunnerRunMetadata{Uuid: uuid.New()}}
	run.Spec.Behavior = behavior
	run.Spec.Definition.Spec.Implementation = client.MeshRunnerRunImplementation{Type: client.MeshRunnerImplementationTypeManual, Manual: &client.MeshRunnerManualImplementation{}}
	return run
}

func writeRunFile(t *testing.T, run client.MeshRunnerRun) string {
	t.Helper()
	testmeshstack.Isolate(t)
	t.Setenv(EnvRunnerUuid, runnerUuid.String())
	encoded, err := json.Marshal(run)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "run.yml")
	require.NoError(t, os.WriteFile(path, encoded, 0o600))
	return path
}

func Test_RunFile_ExitCodeTellsWhetherTheRunDeliveredItsStatus(t *testing.T) {
	for _, behavior := range []string{"succeed", "fail"} {
		t.Run("a "+behavior+" status that meshStack refuses is not delivered", func(t *testing.T) {
			server := testmeshstack.NewStatusServer(t)
			server.PatchStatusCode = http.StatusForbidden
			run := manualRun(behavior)
			server.Link(&run)
			path := writeRunFile(t, run)
			t.Setenv(setting.Endpoint.EnvKey(), server.URL)
			t.Setenv(setting.ApiToken.EnvKey(), run.Spec.RunToken)

			assert.Equal(t, ExitUndelivered, RunFile(t.Context(), path))
		})
	}

	t.Run("a run without a runner uuid is not delivered", func(t *testing.T) {
		path := writeRunFile(t, manualRun("succeed"))
		t.Setenv(EnvRunnerUuid, "")

		assert.Equal(t, ExitUndelivered, RunFile(t.Context(), path))
	})

	t.Run("a run type without executor is not delivered", func(t *testing.T) {
		run := manualRun("succeed")
		run.Spec.Definition.Spec.Implementation = client.MeshRunnerRunImplementation{Type: client.MeshRunnerImplementationTypeGithubWorkflow, GithubWorkflow: &client.MeshRunnerGithubImplementation{}}

		assert.Equal(t, ExitUndelivered, RunFile(t.Context(), writeRunFile(t, run)))
	})
}

func Test_StopReason_IsTheLineBeforeStdinCloses(t *testing.T) {
	require.EqualError(t, stopReason(strings.NewReader("the run exceeded the timeout of 2h0m0s\n")), "the run exceeded the timeout of 2h0m0s")
	assert.ErrorIs(t, stopReason(strings.NewReader("")), errStdinClosed)
}

func Test_ExecutorConfig_RoundTripsTheTfConfigThroughTheEnv(t *testing.T) {
	var cfg config.Config
	cfg.Tf = tf.Config{CacheDir: "/cache", SkipHostKeyValidation: true, InitTimeout: xtime.Minutes(90), InitRetries: 4}
	encoded, err := cfg.ExecutorConfig(config.RunnerTypeTf)
	require.NoError(t, err)

	var received tf.Config
	Register(config.RunnerTypeTf, func(cfg tf.Config) (Executor, error) {
		received = cfg
		return fakeExecute, nil
	})
	t.Cleanup(func() { delete(registry, config.RunnerTypeTf) })
	_, err = newExecutor(config.RunnerTypeTf, encoded)

	require.NoError(t, err)
	assert.Equal(t, cfg.Tf, received)
}
