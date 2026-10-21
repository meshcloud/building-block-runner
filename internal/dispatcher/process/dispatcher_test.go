package process

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"uuid"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/client/types/xurl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/config/xtime"
	"github.com/meshcloud/building-block-runner/internal/dispatcher"
	"github.com/meshcloud/building-block-runner/internal/executor"
	"github.com/meshcloud/building-block-runner/internal/testmeshstack"
)

var runnerUuid = uuid.MustParse("98520496-627d-43e6-82da-ce499179ff3f")

// TestMain is the run process when the dispatcher under test starts the test binary.
func TestMain(m *testing.M) {
	executor.Register(config.RunnerTypeManual, func(struct{}) (executor.Executor, error) { return fakeExecute, nil })
	if runYml := os.Getenv(executor.EnvRunYml); runYml != "" {
		os.Exit(executor.RunFile(context.Background(), runYml))
	}
	os.Exit(m.Run())
}

func fakeExecute(ctx context.Context, run executor.Run) error {
	step := run.Reporter.Step("fake", "Fake")
	if err := run.Reporter.Register(ctx); err != nil {
		return err
	}
	return step.Run(func() error { return hangWithGrandchild(ctx) })
}

func hangWithGrandchild(ctx context.Context) error {
	grandchild := exec.CommandContext(context.WithoutCancel(ctx), "sleep", "300")
	if err := grandchild.Start(); err != nil {
		return err
	}
	if err := os.WriteFile(grandchildPidFile(), []byte(strconv.Itoa(grandchild.Process.Pid)), 0o600); err != nil {
		return err
	}
	return grandchild.Wait()
}

func grandchildPidFile() string {
	return filepath.Join(os.TempDir(), "grandchild.pid")
}

func testConfig() config.Config {
	var cfg config.Config
	cfg.Uuid = runnerUuid
	cfg.Api.Url = xurl.MustParsef("http://127.0.0.1:1")
	cfg.Dispatcher.ForTypes = config.RunnerTypes{config.RunnerTypeAll}
	cfg.Dispatcher.Timeout = xtime.Minutes(1)
	cfg.Dispatcher.KillGrace = xtime.Seconds(30)
	return cfg
}

func newRun() dispatcher.Run {
	run := dispatcher.Run{Metadata: client.MeshRunnerRunMetadata{Uuid: uuid.New()}}
	run.Spec.Definition.Spec.Implementation = client.MeshRunnerRunImplementation{Type: client.MeshRunnerImplementationTypeManual, Manual: &client.MeshRunnerManualImplementation{}}
	return run
}

func isolate(t *testing.T) (tmpDir string) {
	t.Helper()
	testmeshstack.Isolate(t)
	t.Setenv("HOME", t.TempDir())
	tmpDir = t.TempDir()
	t.Setenv("TMPDIR", tmpDir)
	return tmpDir
}

func Test_New_RefusesARunnerDirThatOthersCouldControl(t *testing.T) {
	for name, prepare := range map[string]func(t *testing.T, runnerDir string){
		"a symlink": func(t *testing.T, runnerDir string) {
			t.Helper()
			require.NoError(t, os.Symlink(t.TempDir(), runnerDir))
		},
		"a dir others can read": func(t *testing.T, runnerDir string) {
			t.Helper()
			require.NoError(t, os.Mkdir(runnerDir, 0o700))
			require.NoError(t, os.Chmod(runnerDir, 0o755)) //nolint:gosec // the mode under test
		},
	} {
		t.Run(name, func(t *testing.T) {
			tmpDir := isolate(t)
			prepare(t, filepath.Join(tmpDir, "bb-runner-"+runnerUuid.String()))

			_, err := New(t.Context(), testConfig())

			assert.ErrorContains(t, err, "runner dir")
		})
	}
}

func Test_ForTypes_MustBeRunnableByThisBinary(t *testing.T) {
	t.Run("New refuses a runner type without executor", func(t *testing.T) {
		cfg := testConfig()
		cfg.Dispatcher.ForTypes = config.RunnerTypes{config.RunnerTypeTf}

		_, err := New(t.Context(), cfg)

		assert.EqualError(t, err, "this binary cannot run tf runs, set dispatcher.forTypes to the runner types it was built for")
	})

	t.Run("Dispatch refuses a run outside forTypes", func(t *testing.T) {
		cfg := testConfig()
		cfg.Dispatcher.ForTypes = config.RunnerTypes{config.RunnerTypeManual}
		d, err := New(t.Context(), cfg)
		require.NoError(t, err)
		run := newRun()
		run.Spec.Definition.Spec.Implementation = client.MeshRunnerRunImplementation{Type: client.MeshRunnerImplementationTypeTerraform, Terraform: &client.MeshRunnerTerraformImplementation{}}

		assert.EqualError(t, d.Dispatch(t.Context(), run, ""), "this runner does not run tf runs")
	})
}
