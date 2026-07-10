package main

// main_test.go pins the fixed single-run exit-code contract (B11, PLAN_DETAIL_02 §7 R12):
// executeSingleRun must return a non-zero code for failures before the run's first potentially
// state-mutating step (workdir setup, run-JSON parse, registration) and 0 once the run has begun
// (registration succeeded — even if everything after that, e.g. the source clone, fails). This is
// deliberately narrower than "any failure exits non-zero": the controller's k8s Job template uses
// BackoffLimit:1 + RestartPolicy:Never, so a blanket non-zero exit would make k8s silently
// re-run a failed terraform run once.

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/tf"
)

// writeRunJsonFixture marshals a minimal, valid RunDetailsDTO (APPLY behavior, a Terraform
// implementation pointing at repositoryUrl) to a temp file and returns its path.
func writeRunJsonFixture(t *testing.T, repositoryUrl string) string {
	t.Helper()

	impl := meshapi.TerraformImplementation{
		Type:             "TERRAFORM",
		TerraformVersion: "1.7.0",
		RepositoryUrl:    repositoryUrl,
	}
	implRaw, err := json.Marshal(impl)
	require.NoError(t, err)

	dto := meshapi.RunDetailsDTO{
		ApiVersion: "v1",
		Kind:       "MeshBuildingBlockRun",
		Metadata:   meshapi.RunMetaDTO{Uuid: "run-b11-test"},
		Spec: meshapi.RunSpecDTO{
			Behavior: "APPLY",
			BuildingBlock: meshapi.BuildingBlockSpecDTO{
				Uuid: "bb-b11-test",
				Spec: meshapi.BuildingBlockDetailsSpecDTO{
					DisplayName:         "b11-test-block",
					WorkspaceIdentifier: "ws-b11-test",
				},
			},
			Definition: meshapi.DefinitionSpecDTO{
				Uuid: "def-b11-test",
				Spec: meshapi.DefinitionDetailsSpecDTO{
					Implementation: implRaw,
				},
			},
			RunToken: "test-run-token",
		},
		Links: meshapi.LinksDTO{
			MeshstackBaseUrl: meshapi.LinkDTO{Href: "https://meshstack.example.com"},
		},
	}

	data, err := json.Marshal(dto)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "run.json")
	require.NoError(t, os.WriteFile(path, data, 0600))
	return path
}

// fixedStatusServer returns an httptest server that answers every request with the given status
// code — enough for the register (POST) and status (PATCH) endpoints RunApiClient calls.
func fixedStatusServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func withTestAppConfig(t *testing.T, runApiUrl string) {
	t.Helper()
	previous := tf.AppConfig
	t.Cleanup(func() { tf.AppConfig = previous })
	tf.AppConfig = tf.TfRunnerConfig{
		RunnerUuid:           "b11-test-runner",
		TfParentWorkingDir:   t.TempDir(),
		TfCommandTimeoutMins: 1,
		WsTimeoutMins:        1,
		InitTimeoutMins:      1,
		RunApiBackend:        tf.RunApiConfig{Url: runApiUrl},
	}
}

func Test_ExecuteSingleRun_MissingRunJsonFilePathEnv_ExitsNonZero(t *testing.T) {
	t.Setenv(ENV_RUN_JSON_FILE_PATH, "")
	withTestAppConfig(t, "")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tfbin, err := tf.ForTestNewTfBin(t.TempDir(), io.Discard, nil)
	require.NoError(t, err)

	code := executeSingleRun(logger, tfbin, tf.NoopDecryptor{})

	require.Equal(t, 1, code, "a pre-flight failure (before registration) must exit non-zero")
}

func Test_ExecuteSingleRun_MalformedRunJson_ExitsNonZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.json")
	require.NoError(t, os.WriteFile(path, []byte("not valid json"), 0600))
	t.Setenv(ENV_RUN_JSON_FILE_PATH, path)
	withTestAppConfig(t, "")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tfbin, err := tf.ForTestNewTfBin(t.TempDir(), io.Discard, nil)
	require.NoError(t, err)

	code := executeSingleRun(logger, tfbin, tf.NoopDecryptor{})

	require.Equal(t, 1, code, "malformed run JSON is a pre-flight failure and must exit non-zero")
}

func Test_ExecuteSingleRun_RegistrationFails_ExitsNonZero(t *testing.T) {
	srv := fixedStatusServer(t, http.StatusInternalServerError)
	withTestAppConfig(t, srv.URL)

	runJsonPath := writeRunJsonFixture(t, "/nonexistent/repo/does/not/matter/here")
	t.Setenv(ENV_RUN_JSON_FILE_PATH, runJsonPath)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tfbin, err := tf.ForTestNewTfBin(t.TempDir(), io.Discard, nil)
	require.NoError(t, err)

	code := executeSingleRun(logger, tfbin, tf.NoopDecryptor{})

	require.Equal(t, 1, code, "registration is still before tofu init/apply begins and must exit non-zero on failure")
}

func Test_ExecuteSingleRun_RegistrationSucceedsThenSourceCloneFails_ExitsZero(t *testing.T) {
	srv := fixedStatusServer(t, http.StatusOK)
	withTestAppConfig(t, srv.URL)

	// A repositoryUrl that cannot be cloned makes the run fail *after* registration succeeded —
	// exactly the "run has begun" bucket that must keep exit 0 (the k8s Job must not be re-run
	// automatically; see the B11 fix doc comment on executeSingleRun).
	runJsonPath := writeRunJsonFixture(t, filepath.Join(t.TempDir(), "does-not-exist-as-a-repo"))
	t.Setenv(ENV_RUN_JSON_FILE_PATH, runJsonPath)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tfbin, err := tf.ForTestNewTfBin(t.TempDir(), io.Discard, nil)
	require.NoError(t, err)

	code := executeSingleRun(logger, tfbin, tf.NoopDecryptor{})

	require.Equal(t, 0, code, "once registration succeeds, a later failure (e.g. source clone) must not flip the exit code")
}
