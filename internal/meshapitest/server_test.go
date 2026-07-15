package meshapitest

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/meshapi"
)

func testRun(uuid string) *meshapi.RunDetailsDTO {
	return &meshapi.RunDetailsDTO{
		ApiVersion: "v1",
		Kind:       "MeshBuildingBlockRun",
		Metadata:   meshapi.RunMetaDTO{Uuid: uuid},
		Spec: meshapi.RunSpecDTO{
			RunNumber: 1,
			Behavior:  "APPLY",
			RunToken:  "test-run-token",
		},
	}
}

// Test_ClaimCycle_FullCycleOverRealTransport drives claim -> register -> PATCH -> terminal
// PATCH through the real meshapi.Client dialing this mock over real HTTP (the preferred
// style) rather than a hand-rolled http.RoundTripper.
func Test_ClaimCycle_FullCycleOverRealTransport(t *testing.T) {
	srv := NewServer(t)
	srv.SeedRun(testRun("run-1"))

	client := meshapi.NewClientWithHTTP(srv.URL, "test-node", meshapi.BearerTokenAuth{Token: "runner-token"}, srv.Client())

	dto, raw, err := client.FetchRun("runner-uuid-1")
	require.NoError(t, err)
	assert.Equal(t, "run-1", dto.Metadata.Uuid)
	assert.Contains(t, string(raw), "run-1")

	err = client.RegisterSource(dto.Metadata.Uuid, meshapi.RegistrationDTO{
		Source: meshapi.SourceDTO{Id: "tf"},
		Steps: []meshapi.StepRegistrationDTO{
			{Id: "apply", DisplayName: "Apply"},
		},
	})
	require.NoError(t, err)

	inProgress := "IN_PROGRESS"
	body, err := client.PatchStatus(dto.Metadata.Uuid, "tf", meshapi.StatusUpdateDTO{Status: &inProgress})
	require.NoError(t, err)
	assert.JSONEq(t, `{"runAborted":false}`, string(body))

	succeeded := "SUCCEEDED"
	_, err = client.PatchStatus(dto.Metadata.Uuid, "tf", meshapi.StatusUpdateDTO{Status: &succeeded})
	require.NoError(t, err)

	// Assert on what the mock captured, over the wire, not on in-process call args.
	claims := srv.Claims()
	require.Len(t, claims, 1)
	assert.Equal(t, "runner-uuid-1", claims[0].RunnerUuid)
	assert.Equal(t, "test-node", claims[0].Header.Get("X-Block-Runner-Node-Id"))
	assert.Equal(t, "Bearer runner-token", claims[0].Header.Get("Authorization"))

	registers := srv.Registers()
	require.Len(t, registers, 1)
	assert.Equal(t, "run-1", registers[0].RunId)
	assert.Equal(t, "tf", registers[0].Registration.Source.Id)
	require.Len(t, registers[0].Registration.Steps, 1)
	assert.Equal(t, "apply", registers[0].Registration.Steps[0].Id)

	patches := srv.Patches()
	require.Len(t, patches, 2)
	assert.Equal(t, "run-1", patches[0].RunId)
	assert.Equal(t, "tf", patches[0].SourceId)
	assert.Contains(t, string(patches[0].Body), "IN_PROGRESS")
	assert.Contains(t, string(patches[1].Body), "SUCCEEDED")
}

// Test_Claim_QueueIsFifoAndEmptyMeansNoRun proves the seeded-run queue drains in order and
// an empty queue reproduces the meshfed "no pending run" response (default 404), which is
// one of the two pinned "no run" statuses on the claim endpoint.
func Test_Claim_QueueIsFifoAndEmptyMeansNoRun(t *testing.T) {
	srv := NewServer(t)
	srv.SeedRun(testRun("run-a"))
	srv.SeedRun(testRun("run-b"))

	client := meshapi.NewClientWithHTTP(srv.URL, "node", meshapi.BearerTokenAuth{Token: "t"}, srv.Client())

	dto1, _, err := client.FetchRun("runner")
	require.NoError(t, err)
	assert.Equal(t, "run-a", dto1.Metadata.Uuid)

	dto2, _, err := client.FetchRun("runner")
	require.NoError(t, err)
	assert.Equal(t, "run-b", dto2.Metadata.Uuid)

	_, _, err = client.FetchRun("runner")
	require.Error(t, err, "queue is drained, claim should report no run")
	var httpErr meshapi.HttpError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusNotFound, httpErr.StatusCode)
	assert.True(t, httpErr.IsNotFound())
}

// Test_Claim_NoRunStatusOverride proves the 409-on-claim = no run branch is reachable
// too, not just the default 404.
func Test_Claim_NoRunStatusOverride(t *testing.T) {
	srv := NewServer(t, WithNoRunStatus(http.StatusConflict))

	client := meshapi.NewClientWithHTTP(srv.URL, "node", meshapi.BearerTokenAuth{Token: "t"}, srv.Client())

	_, _, err := client.FetchRun("runner")
	require.Error(t, err)
	var httpErr meshapi.HttpError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusConflict, httpErr.StatusCode)
	assert.True(t, httpErr.IsConflict())
}

// Test_Register_SeedRegisterResponse_Conflict proves 409-on-register is treated as success
// by the real client (the "409-on-register = success" pin), driven from a seeded response.
func Test_Register_SeedRegisterResponse_Conflict(t *testing.T) {
	srv := NewServer(t)
	srv.SeedRegisterResponse(http.StatusConflict)

	client := meshapi.NewClientWithHTTP(srv.URL, "node", meshapi.BearerTokenAuth{Token: "t"}, srv.Client())

	err := client.RegisterSource("run-1", meshapi.RegistrationDTO{Source: meshapi.SourceDTO{Id: "tf"}})
	require.NoError(t, err, "409 on register must be a no-op success")

	require.Len(t, srv.Registers(), 1)
}

// Test_Patch_SeedPatchResponse_AbortAndAlreadyAborted proves both abort-signalling shapes
// are pinned: a live abort flag on a 200, and the already-aborted 409 {runAborted:true}.
func Test_Patch_SeedPatchResponse_AbortAndAlreadyAborted(t *testing.T) {
	srv := NewServer(t)
	srv.SeedPatchResponse(PatchResponse{Status: http.StatusOK, Abort: true})
	srv.SeedPatchResponse(PatchResponse{Status: http.StatusConflict, Abort: true})

	client := meshapi.NewClientWithHTTP(srv.URL, "node", meshapi.BearerTokenAuth{Token: "t"}, srv.Client())

	body, err := client.PatchStatus("run-1", "tf", meshapi.StatusUpdateDTO{})
	require.NoError(t, err)
	assert.JSONEq(t, `{"runAborted":true}`, string(body))

	// PatchStatus treats any non-2xx as an error, so the caller inspects the error's body
	// for the already-aborted marker; the mock does not reinterpret that for the client.
	_, err = client.PatchStatus("run-1", "tf", meshapi.StatusUpdateDTO{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "runAborted")
}

// Test_Artifact_SeedAndDownloadRoundTrips proves SeedArtifact's URL is a working download
// target for meshapi.Client.DownloadArtifact, including the 128MiB-cap streaming path.
func Test_Artifact_SeedAndDownloadRoundTrips(t *testing.T) {
	srv := NewServer(t)
	payload := []byte("a saved terraform plan")
	url := srv.SeedArtifact("plan-1", payload)

	client := meshapi.NewClientWithHTTP(srv.URL, "node", meshapi.BearerTokenAuth{Token: "t"}, srv.Client())

	var buf bytes.Buffer
	err := client.DownloadArtifact(url, &buf)
	require.NoError(t, err)
	assert.Equal(t, payload, buf.Bytes())
}

// Test_Artifact_UnseededIdIs404 proves an id nobody seeded fails visibly rather than
// silently serving an empty artifact.
func Test_Artifact_UnseededIdIs404(t *testing.T) {
	srv := NewServer(t)
	client := meshapi.NewClientWithHTTP(srv.URL, "node", meshapi.BearerTokenAuth{Token: "t"}, srv.Client())

	var buf bytes.Buffer
	err := client.DownloadArtifact(srv.URL+"/artifacts/does-not-exist", &buf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}
