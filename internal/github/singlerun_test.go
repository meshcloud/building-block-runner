package github

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

// githubRunDTO builds a decrypted (single-run) github run DTO pointing at baseURL.
func githubRunDTO(t *testing.T, baseURL, appPem string, async bool) *meshapi.RunDetailsDTO {
	t.Helper()
	impl := meshapi.GithubImplementation{
		Type: "GITHUB_WORKFLOW", GithubBaseUrl: baseURL, Owner: "owner", AppId: "123",
		AppPem: appPem, Repository: "repo", Branch: "main", ApplyWorkflow: "apply.yml", Async: async,
	}
	raw, err := json.Marshal(impl)
	require.NoError(t, err)
	return &meshapi.RunDetailsDTO{
		Kind: "meshBuildingBlockRun", ApiVersion: "v1",
		Metadata: meshapi.RunMetaDTO{Uuid: "run-sr"},
		Spec: meshapi.RunSpecDTO{
			Behavior: "APPLY", RunToken: "file-token",
			Definition: meshapi.DefinitionSpecDTO{Spec: meshapi.DefinitionDetailsSpecDTO{Implementation: raw}},
		},
		Status: "IN_PROGRESS",
		Links:  meshapi.LinksDTO{Self: meshapi.LinkDTO{Href: "https://mesh/run/run-sr"}},
	}
}

// TestSingleRun_Github_AsyncHandover drives the k8s single-run async path: full GitHub auth
// chain + dispatch against the stub, reporting against meshapitest, exit 0 (the handover IS
// the job success under R12).
func TestSingleRun_Github_AsyncHandover(t *testing.T) {
	stub := newGithubStub(t)
	srv := meshapitest.NewServer(t)

	dto := githubRunDTO(t, stub.url(), singleLinePem(t), true)
	raw, err := json.Marshal(dto)
	require.NoError(t, err)
	t.Setenv(envRunJsonFilePath, writeRunFile(t, raw))

	code := RunSingleRun(context.Background(), testLog(),
		Config{Uuid: "runner", Version: "test", Api: config.Api{Url: srv.URL}},
		meshapi.Identity{Name: "github-block-runner"})
	require.Equal(t, 0, code)

	require.Len(t, srv.Registers(), 1)
	patches := srv.Patches()
	require.Len(t, patches, 1)
	require.Equal(t, "Bearer file-token", patches[0].Header.Get("Authorization"))
}

// TestSingleRun_MissingPath: no RUN_JSON_FILE_PATH ⇒ exit 1 (the §7.9 tightening, G-P11).
func TestSingleRun_MissingPath(t *testing.T) {
	require.Equal(t, 1, RunSingleRun(context.Background(), testLog(), Config{Uuid: "r"}, meshapi.Identity{}))
}

// TestSingleRun_ParseError: unparsable run file ⇒ exit 1.
func TestSingleRun_ParseError(t *testing.T) {
	t.Setenv(envRunJsonFilePath, writeRunFile(t, []byte("{not json")))
	require.Equal(t, 1, RunSingleRun(context.Background(), testLog(), Config{Uuid: "r", Api: config.Api{Url: "http://unused"}}, meshapi.Identity{}))
}

// TestSingleRun_ReportFailure: a report transport failure ⇒ exit 1 (G-P11 report-failure leg).
func TestSingleRun_ReportFailure(t *testing.T) {
	stub := newGithubStub(t)
	srv := meshapitest.NewServer(t)
	srv.SeedPatchResponse(meshapitest.PatchResponse{Status: 500})

	dto := githubRunDTO(t, stub.url(), singleLinePem(t), true)
	raw, err := json.Marshal(dto)
	require.NoError(t, err)
	t.Setenv(envRunJsonFilePath, writeRunFile(t, raw))

	require.Equal(t, 1, RunSingleRun(context.Background(), testLog(),
		Config{Uuid: "runner", Api: config.Api{Url: srv.URL}}, meshapi.Identity{Name: "github-block-runner"}))
}
