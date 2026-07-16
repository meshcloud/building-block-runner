package dispatch_test

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/meshapitest"
)

// Test_InProcess_ConcurrentRuns_StatusUpdatesDoNotInterleave pins the concurrency invariant:
// concurrent runs must never let one run's status snapshot bleed into
// another's PATCH body -- the exact failure mode a shared/mutable status struct would
// produce. Each run sends several PATCHes
// against the real meshapitest mock server, deliberately yielding between them
// (runtime.Gosched) to widen the interleaving window with other concurrent runs' PATCHes.
func Test_InProcess_ConcurrentRuns_StatusUpdatesDoNotInterleave(t *testing.T) {
	server := meshapitest.NewServer(t)

	const stepsPerRun = 3

	handler := concurrencyTestHandlerFunc(func(ctx context.Context, run dispatch.ClaimedRun) error {
		client := meshapi.NewRunClient(server.URL, "node-"+string(run.Id),
			meshapi.BearerTokenAuth{Token: run.Run.Spec.RunToken})

		reg := meshapi.RegistrationDTO{
			Source: meshapi.SourceDTO{Id: string(run.Id)},
			Steps:  []meshapi.StepRegistrationDTO{{Id: "apply", DisplayName: "Apply"}},
		}
		if err := client.RegisterSource(string(run.Id), reg); err != nil {
			return err
		}

		for step := 0; step < stepsPerRun; step++ {
			status := "IN_PROGRESS"
			// Each PATCH's payload embeds this run's own id and step index -- a shared
			// mutable status object mutated by another goroutine between construction and
			// send would show up here as a body whose content does not match the URL
			// (server-captured PatchRequest.RunId) it was sent under.
			summary := fmt.Sprintf("run %s step %d", run.Id, step)
			msg := summary
			body := meshapi.StatusUpdateDTO{
				Status:  &status,
				Summary: &summary,
				Steps: []meshapi.StepStatusUpdateDTO{
					{Id: "apply", DisplayName: "Apply", Status: &status, SystemMessage: &msg},
				},
			}
			if _, err := client.PatchStatus(string(run.Id), string(run.Id), body); err != nil {
				return err
			}
			runtime.Gosched()
		}
		return nil
	})

	in, err := dispatch.NewInProcess(
		map[meshapi.RunnerImplementationType]dispatch.RunHandler{meshapi.RunnerTypeTerraform: handler},
		time.Second, discardLogger())
	require.NoError(t, err)

	const n = 6
	for i := 0; i < n; i++ {
		require.NoError(t, in.Dispatch(newClaimedRun(fmt.Sprintf("run-%d", i), fmt.Sprintf("tok-%d", i))))
	}
	in.Wait()

	patches := server.Patches()
	require.Len(t, patches, n*stepsPerRun, "every run's every step PATCH must have reached the server")

	for _, p := range patches {
		var dto meshapi.StatusUpdateDTO
		require.NoError(t, json.Unmarshal(p.Body, &dto))
		require.NotNil(t, dto.Summary)
		assert.Contains(t, *dto.Summary, p.RunId,
			"PATCH body for run %s must reference only its own run id, got summary %q", p.RunId, *dto.Summary)

		for otherRunIdx := 0; otherRunIdx < n; otherRunIdx++ {
			other := fmt.Sprintf("run-%d", otherRunIdx)
			if other == p.RunId {
				continue
			}
			assert.NotContains(t, *dto.Summary, other,
				"PATCH body sent under run %s must never mention another run (%s): %q", p.RunId, other, *dto.Summary)
		}
	}
}
