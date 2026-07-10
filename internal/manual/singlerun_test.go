package manual

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

func writeRunFile(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "run.json")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

func cfgApi(url string) config.Api { return config.Api{Url: url} }

// TestSingleRun_Happy drives the k8s single-run path from a mounted file: exit 0 and the
// pinned register+update wire, using the run's own runToken (Scenario_Manual_SingleRun).
func TestSingleRun_Happy(t *testing.T) {
	srv := meshapitest.NewServer(t)
	dto := &meshapi.RunDetailsDTO{
		Metadata: meshapi.RunMetaDTO{Uuid: testUuid},
		Spec: meshapi.RunSpecDTO{
			RunToken:      "file-token",
			BuildingBlock: meshapi.BuildingBlockSpecDTO{Spec: meshapi.BuildingBlockDetailsSpecDTO{Inputs: []meshapi.BuildingBlockInputSpecDTO{{Key: "k", Value: "v", Type: typeString}}}},
		},
	}
	raw, err := json.Marshal(dto)
	require.NoError(t, err)
	t.Setenv(envRunJsonFilePath, writeRunFile(t, raw))

	code := RunSingleRun(context.Background(), testLog(), Config{Uuid: testUuid, Version: "test", Api: cfgApi(srv.URL)}, meshapi.Identity{Name: "manual-block-runner"})
	require.Equal(t, 0, code)

	require.Len(t, srv.Registers(), 1)
	patches := srv.Patches()
	require.Len(t, patches, 1)
	require.Equal(t, "Bearer file-token", patches[0].Header.Get("Authorization"))
}

// TestSingleRun_MissingPath pins M-P7's Go delta: no RUN_JSON_FILE_PATH ⇒ exit 1 (the
// deliberate tightening of the Kotlin exit-0 swallow, umbrella §7.9/§10.3).
func TestSingleRun_MissingPath(t *testing.T) {
	require.Equal(t, 1, RunSingleRun(context.Background(), testLog(), Config{Uuid: testUuid}, meshapi.Identity{}))
}

// TestSingleRun_ParseError pins the parse-failure ⇒ exit 1 tightening.
func TestSingleRun_ParseError(t *testing.T) {
	t.Setenv(envRunJsonFilePath, writeRunFile(t, []byte("{not json")))
	require.Equal(t, 1, RunSingleRun(context.Background(), testLog(), Config{Uuid: testUuid, Api: cfgApi("http://unused")}, meshapi.Identity{}))
}

// TestSingleRun_ReportFailure pins M-P6: a report transport failure ⇒ exit 1.
func TestSingleRun_ReportFailure(t *testing.T) {
	srv := meshapitest.NewServer(t)
	srv.SeedPatchResponse(meshapitest.PatchResponse{Status: 500})
	dto := &meshapi.RunDetailsDTO{Metadata: meshapi.RunMetaDTO{Uuid: testUuid}, Spec: meshapi.RunSpecDTO{RunToken: "t"}}
	raw, err := json.Marshal(dto)
	require.NoError(t, err)
	t.Setenv(envRunJsonFilePath, writeRunFile(t, raw))
	require.Equal(t, 1, RunSingleRun(context.Background(), testLog(), Config{Uuid: testUuid, Api: cfgApi(srv.URL)}, meshapi.Identity{}))
}
