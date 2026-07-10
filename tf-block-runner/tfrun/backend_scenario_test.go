package tfrun

// backend_scenario_test.go pins the meshStack HTTP backend fallback use case end to end
// (PLAN_DETAIL_01_tf_characterization_tests.md CP6, D9 pin 9) plus the two matrix rows that live in
// the same code path: the env whitelist (cleanSystemEnv, D9 pin 13) and the init-retry behavior
// (tfcmd.go:170-184; B4's *buggy sleep duration* is pinned separately in bug_inventory_test.go —
// this file only pins that the retry itself succeeds). Driven through TfApplyCommand.execute() end
// to end (real GitSource + hermetic local-repo clone, CP1) rather than Worker/SingleRunWorker,
// because the backend/env logic has no HTTP-transport-observable surface of its own — the
// Worker/SingleRunWorker matrix rows already prove the surrounding wiring.
//
// HINT_INIT_FAILED's APPLY/DETECT/DESTROY asymmetry (bug B13) is pinned in bug_inventory_test.go,
// not duplicated here.

import (
	"context"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// backendFallbackFixture is the result of driving one APPLY run to completion against a hermetic
// local git repo; it exposes everything the CP6 assertions need to inspect: the run's final status,
// the working directory (for the generated backend .tf file), and whatever env map reached the
// TfFacade (for the TF_HTTP_*/whitelist pins).
type backendFallbackFixture struct {
	runContextInfo *RunContextInfo
	capturedEnv    map[string]string
}

// runBackendFallbackScenario builds a real Run (hermetic local-repo GitSource, CP1) and drives a
// full TfApplyCommand.execute() against it, capturing the env map handed to TfFacade.SetEnv via the
// MockedTfFacade's setEnvFunc hook (mockedtffacade.go — test-infra extension, CP4-CP6).
func runBackendFallbackScenario(t *testing.T, repoFiles map[string]string, useMeshBackendFallback bool, runToken, meshstackBaseUrl string) *backendFallbackFixture {
	t.Helper()

	previous := AppConfig
	t.Cleanup(func() { AppConfig = previous })
	AppConfig = TfRunnerConfig{
		RunnerUuid:           "backend-fallback-scenario",
		InitTimeoutMins:      1,
		WsTimeoutMins:        1,
		TfCommandTimeoutMins: 1,
	}

	repo := makeLocalGitRepo(t, repoFiles)

	run := &Run{
		Id:                     "backend-fallback-run",
		BuildingBlockId:        "bb-backend",
		BuildingBlockName:      "backend-fallback-test",
		TerraformVersion:       DEFAULT_TF_VER,
		Behavior:               APPLY,
		WorkspaceIdentifier:    p("ws-backend"),
		Vars:                   map[string]*Variable{},
		Source:                 &GitSource{url: repo.Path, auth: &NoAuth{}, gitFacade: &Git{}},
		UseMeshBackendFallback: useMeshBackendFallback,
		RunToken:               runToken,
		MeshstackBaseUrl:       meshstackBaseUrl,
	}

	wd := t.TempDir()
	require.NoError(t, os.Mkdir(path.Join(wd, "logs"), 0700))
	runContextInfo := initRunContextInfo(run, "[backend-fallback] ", io.Discard, wd)
	run.Source.setLog(runContextInfo.logwrap)
	ctx := context.Background()

	params := &TfCmdParams{
		dir:                wd,
		buildingBlockId:    run.BuildingBlockId,
		tfVersion:          run.TerraformVersion,
		useWorkspaces:      true,
		suggestedWorkspace: run.toWorkspaceStr(),
		vars:               run.Vars,
		source:             run.Source,
		runMode:            run.Behavior.str(),
	}

	mock := &MockedTfFacade{}
	mock.initMockFuncs()
	var capturedEnv map[string]string
	mock.setEnvFunc = func(env map[string]string) error {
		capturedEnv = env
		return nil
	}
	tfbin, err := ForTestNewTfBin(t.TempDir(), io.Discard, mock)
	require.NoError(t, err)

	tfCmd := ApplyCmd(ctx, runContextInfo, params, tfbin, nil)
	tfCmd.initRunSteps()
	tfCmd.execute()

	return &backendFallbackFixture{runContextInfo: runContextInfo, capturedEnv: capturedEnv}
}

// findBackendFile returns the content of the single meshStack_httpbackend-*.tf file in dir, or
// ("", false) if none exists.
func findBackendFile(t *testing.T, dir string) (string, bool) {
	t.Helper()

	var content string
	var found bool
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "meshStack_httpbackend") {
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			require.NoError(t, err)
			content = string(data)
			found = true
		}
	}
	return content, found
}

func Test_BackendFallback_UseCaseMatrix(t *testing.T) {
	const runToken = "backend-fallback-run-token"

	t.Run("fallback on, no backend block in sources: writes http backend file, disables workspaces, sets TF_HTTP_* env", func(t *testing.T) {
		f := runBackendFallbackScenario(t, map[string]string{
			"main.tf": "# no terraform/backend block\n",
		}, true, runToken, "https://meshstack.example.com")

		require.Equal(t, SUCCEEDED, f.runContextInfo.runStatus.Status)

		content, found := findBackendFile(t, f.runContextInfo.workingDirectory)
		require.True(t, found, "expected a meshStack_httpbackend-*.tf file to be written")
		assert.Contains(t, content, "https://meshstack.example.com/api/terraform/state/workspace/ws-backend/buildingBlock/bb-backend")

		require.NotNil(t, f.capturedEnv)
		assert.Equal(t, MeshStackRunTokenBasicUser, f.capturedEnv["TF_HTTP_USERNAME"])
		assert.Equal(t, runToken, f.capturedEnv["TF_HTTP_PASSWORD"])
	})

	t.Run("fallback on, existing backend block in sources: no file written, workspaces stay on, logs existing-backend notice", func(t *testing.T) {
		f := runBackendFallbackScenario(t, map[string]string{
			"main.tf": "terraform {\n  backend \"local\" {}\n}\n",
		}, true, runToken, "https://meshstack.example.com")

		require.Equal(t, SUCCEEDED, f.runContextInfo.runStatus.Status)

		_, found := findBackendFile(t, f.runContextInfo.workingDirectory)
		assert.False(t, found, "an existing backend must not be overwritten with the meshStack http backend")

		logs, err := os.ReadFile(f.runContextInfo.logFile_name)
		require.NoError(t, err)
		assert.Contains(t, string(logs), "Using existing backend.")

		// TF_HTTP_* auth is still supplied (harmless: only consulted by the http backend, which
		// isn't in play here) — useMeshBackendFallback+runToken alone gate it in buildTfEnv.
		require.NotNil(t, f.capturedEnv)
		assert.Equal(t, runToken, f.capturedEnv["TF_HTTP_PASSWORD"])
	})

	t.Run("meshstackBaseUrl empty falls back to AppConfig.RunApiBackend.Url", func(t *testing.T) {
		previous := AppConfig
		t.Cleanup(func() { AppConfig = previous })
		AppConfig = TfRunnerConfig{
			RunnerUuid:           "backend-fallback-scenario",
			InitTimeoutMins:      1,
			WsTimeoutMins:        1,
			TfCommandTimeoutMins: 1,
			RunApiBackend:        RunApiConfig{Url: "https://fallback-from-appconfig.example.com"},
		}

		cmd := &GenericTfCmd{
			ctx: context.Background(),
			runContextInfo: &RunContextInfo{
				bbId:                "bb-empty-baseurl",
				workspaceIdentifier: "ws-empty-baseurl",
				runToken:            runToken,
				meshstackBaseUrl:    "", // deliberately empty: must fall back to AppConfig
				workingDirectory:    t.TempDir(),
				logwrap:             NewLogWrap(log.New(io.Discard, "", log.LstdFlags), "/dev/null"),
			},
		}

		require.NoError(t, cmd.createMeshStackHttpBackendFile())

		content, found := findBackendFile(t, cmd.runContextInfo.workingDirectory)
		require.True(t, found)
		assert.Contains(t, content, "https://fallback-from-appconfig.example.com/api/terraform/state/workspace/ws-empty-baseurl/buildingBlock/bb-empty-baseurl")
	})

	t.Run("env whitelist: an ambient credential-shaped var never reaches the tf subprocess env", func(t *testing.T) {
		t.Setenv("AWS_SECRET_ACCESS_KEY", "poisoned-ambient-secret")

		f := runBackendFallbackScenario(t, map[string]string{
			"main.tf": "# no backend block\n",
		}, true, runToken, "https://meshstack.example.com")

		require.Equal(t, SUCCEEDED, f.runContextInfo.runStatus.Status)
		require.NotNil(t, f.capturedEnv)
		assert.NotContains(t, f.capturedEnv, "AWS_SECRET_ACCESS_KEY",
			"cleanSystemEnv must not let an unlisted ambient env var reach the tf subprocess")
	})

	t.Run("init retry: a transient first init failure is retried once and the run still succeeds", func(t *testing.T) {
		previous := AppConfig
		t.Cleanup(func() { AppConfig = previous })
		AppConfig = TfRunnerConfig{RunnerUuid: "init-retry-scenario", InitTimeoutMins: 1, WsTimeoutMins: 1, TfCommandTimeoutMins: 1}

		mock := &MockedTfFacade{}
		mock.initMockFuncs()
		calls := 0
		mock.initFunc = func(ctx context.Context, opts ...tfexec.InitOption) error {
			calls++
			if calls == 1 {
				return assert.AnError
			}
			return nil
		}

		cmd := &GenericTfCmd{
			ctx: context.Background(),
			params: &TfCmdParams{
				useWorkspaces: false, // isolate the retry behavior from workspace selection
			},
			runContextInfo: &RunContextInfo{
				logwrap: NewLogWrap(log.New(io.Discard, "", log.LstdFlags), "/dev/null"),
			},
		}

		require.NoError(t, cmd.init(mock))
		assert.Equal(t, 2, calls, "expected exactly one retry after the first transient failure")
	})
}
