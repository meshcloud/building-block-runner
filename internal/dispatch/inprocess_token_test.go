package dispatch_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/meshapitest"
)

// Test_InProcess_ConcurrentRuns_UseOwnRunTokens pins the concurrency invariant: with the
// former single shared mutable run-token slot gone, each run's
// handler builds its own runToken-only meshapi.RunClient -- no base-auth fallback, so the
// runner's process credentials can never leak into a run-scoped call, and no run can ever
// authenticate with another run's token. This drives N concurrent runs, each with a
// distinct runToken, through a real meshapitest server and asserts every captured
// register-source/status-PATCH request for a run carries only that run's own Bearer token.
func Test_InProcess_ConcurrentRuns_UseOwnRunTokens(t *testing.T) {
	server := meshapitest.NewServer(t)

	handler := concurrencyTestHandlerFunc(func(ctx context.Context, run dispatch.ClaimedRun) error {
		// Mirrors the per-run, runToken-only client the real handler ports must build
		// built fresh per run, from run.Details.Spec.RunToken alone.
		client := meshapi.NewRunClient(server.URL, "node-"+string(run.Id),
			meshapi.BearerTokenAuth{Token: run.Details.Spec.RunToken})

		reg := meshapi.RegistrationDTO{
			Source: meshapi.SourceDTO{Id: string(run.Id)},
			Steps:  []meshapi.StepRegistrationDTO{{Id: "apply", DisplayName: "Apply"}},
		}
		if err := client.RegisterSource(string(run.Id), reg); err != nil {
			return err
		}

		status, summary := "SUCCEEDED", "done"
		_, err := client.PatchStatus(string(run.Id), string(run.Id), meshapi.StatusUpdateDTO{
			Status: &status, Summary: &summary,
		})
		return err
	})

	in, err := dispatch.NewInProcess(
		map[meshapi.RunnerImplementationType]dispatch.RunHandler{meshapi.RunnerTypeTerraform: handler},
		time.Second, discardLogger())
	require.NoError(t, err)

	const n = 8
	for i := 0; i < n; i++ {
		run := newClaimedRun(fmt.Sprintf("run-%d", i), fmt.Sprintf("secret-token-%d", i))
		require.NoError(t, in.Dispatch(run))
	}
	in.Wait()

	registers := server.Registers()
	patches := server.Patches()
	require.Len(t, registers, n)
	require.Len(t, patches, n)

	authHeadersSeen := map[string]bool{}
	for i := 0; i < n; i++ {
		runId := fmt.Sprintf("run-%d", i)
		wantAuth := "Bearer secret-token-" + fmt.Sprint(i)

		reg := findRegisterByRunId(t, registers, runId)
		assert.Equal(t, wantAuth, reg.Header.Get("Authorization"),
			"register-source call for run %s must carry only its own runToken", runId)

		patch := findPatchByRunId(t, patches, runId)
		assert.Equal(t, wantAuth, patch.Header.Get("Authorization"),
			"status PATCH for run %s must carry only its own runToken", runId)

		authHeadersSeen[wantAuth] = true
	}
	assert.Len(t, authHeadersSeen, n, "every run must have used a distinct Authorization header -- no cross-run token reuse")
}

func findRegisterByRunId(t *testing.T, reqs []meshapitest.RegisterRequest, runId string) meshapitest.RegisterRequest {
	t.Helper()
	for _, r := range reqs {
		if r.RunId == runId {
			return r
		}
	}
	t.Fatalf("no register-source request captured for run %s", runId)
	return meshapitest.RegisterRequest{}
}

func findPatchByRunId(t *testing.T, reqs []meshapitest.PatchRequest, runId string) meshapitest.PatchRequest {
	t.Helper()
	for _, r := range reqs {
		if r.RunId == runId {
			return r
		}
	}
	t.Fatalf("no status PATCH request captured for run %s", runId)
	return meshapitest.PatchRequest{}
}
