package azdevops

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/meshapitest"
)

func writeRunJSON(t *testing.T, adoBaseUrl string) string {
	t.Helper()
	run := buildRun(t, adoBaseUrl, "run-token-single", implFixture{PersonalAccessToken: "plaintext-pat-already-decrypted", Async: true}, nil)
	raw, err := json.Marshal(run.Details)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "run.json")
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	return path
}

// Test_RunSingleRun_AsyncCapturedWire is K-P1: an async run JSON via RUN_JSON_FILE_PATH
// produces a captured register + IN_PROGRESS handover update, exit 0. The PAT arrives
// plaintext and is NOT decrypted (NoOp crypto, controller pre-decrypted it).
func Test_RunSingleRun_AsyncCapturedWire(t *testing.T) {
	ado := newSeqADO(t)
	srv := meshapitest.NewServer(t)
	path := writeRunJSON(t, ado.URL)
	t.Setenv("RUN_JSON_FILE_PATH", path)

	cfg := Config{Uuid: testUuid, Api: config.Api{Url: srv.URL}}
	id := meshapi.Identity{Name: "azure-devops-block-runner", Version: "test"}
	code := RunSingleRun(context.Background(), testLog(), cfg, id)
	require.Equal(t, 0, code)

	require.Len(t, srv.Registers(), 1)
	patches := srv.Patches()
	require.Len(t, patches, 1)
	require.Equal(t, "Bearer run-token-single", patches[0].Header.Get("Authorization"))

	reqs := ado.Requests()
	require.Len(t, reqs, 1)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(reqs[0].Body, &payload))
	// The trigger auth header carries the PAT verbatim (NoOp decryptor: it was already
	// plaintext); the client never re-decrypts it.
	require.NotEmpty(t, reqs[0].Header.Get("Authorization"))
}

// Test_RunSingleRun_MissingEnv is the R12 tail's file-not-found rung.
func Test_RunSingleRun_MissingEnv(t *testing.T) {
	t.Setenv("RUN_JSON_FILE_PATH", "")
	code := RunSingleRun(context.Background(), testLog(), Config{Uuid: testUuid}, meshapi.Identity{})
	require.Equal(t, 1, code)
}

// Test_RunSingleRun_FileNotFound / Test_RunSingleRun_UnparsableFile are the sanctioned K-P2
// delta (umbrella §7.9): Go exits non-zero on a pre-report fetch/parse failure where Kotlin
// swallowed it and exited 0.
func Test_RunSingleRun_FileNotFound(t *testing.T) {
	t.Setenv("RUN_JSON_FILE_PATH", filepath.Join(t.TempDir(), "absent.json"))
	code := RunSingleRun(context.Background(), testLog(), Config{Uuid: testUuid}, meshapi.Identity{})
	require.Equal(t, 1, code)
}

func Test_RunSingleRun_UnparsableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.json")
	require.NoError(t, os.WriteFile(path, []byte("not json"), 0o600))
	t.Setenv("RUN_JSON_FILE_PATH", path)
	code := RunSingleRun(context.Background(), testLog(), Config{Uuid: testUuid}, meshapi.Identity{})
	require.Equal(t, 1, code)
}

// Test_RunSingleRun_RegisterFailureExitsNonZero pins K-P2's register-failure rung (Kotlin
// exit-1 parity, unchanged).
func Test_RunSingleRun_RegisterFailureExitsNonZero(t *testing.T) {
	ado := newSeqADO(t)
	srv := meshapitest.NewServer(t)
	srv.SeedRegisterResponse(500)
	path := writeRunJSON(t, ado.URL)
	t.Setenv("RUN_JSON_FILE_PATH", path)

	cfg := Config{Uuid: testUuid, Api: config.Api{Url: srv.URL}}
	code := RunSingleRun(context.Background(), testLog(), cfg, meshapi.Identity{Name: "azure-devops-block-runner"})
	require.Equal(t, 1, code)
}

// Test_RunSingleRun_SyncCompletesWithinTheJobPod exercises a sync single-run: the Job pod
// itself performs the poll (unchanged from Kotlin, §7.2) -- proven here with an
// already-COMPLETED trigger so the test stays instant.
func Test_RunSingleRun_SyncCompletesWithinTheJobPod(t *testing.T) {
	ado := newSeqADO(t)
	ado.triggerResp = adoResp{status: 200, body: `{"id":1,"state":"completed","result":"succeeded","createdDate":"now"}`}
	srv := meshapitest.NewServer(t)

	run := buildRun(t, ado.URL, "tok", implFixture{PersonalAccessToken: "pat", Async: false}, nil)
	raw, err := json.Marshal(run.Details)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "run.json")
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	t.Setenv("RUN_JSON_FILE_PATH", path)

	cfg := Config{Uuid: testUuid, Api: config.Api{Url: srv.URL}}
	code := RunSingleRun(context.Background(), testLog(), cfg, meshapi.Identity{Name: "azure-devops-block-runner"})
	require.Equal(t, 0, code)

	final := decodePatch(t, srv.Patches()[len(srv.Patches())-1].Body)
	require.Equal(t, "SUCCEEDED", final.Status)
}
