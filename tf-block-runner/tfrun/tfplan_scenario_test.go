package tfrun

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"

	"github.com/hashicorp/terraform-exec/tfexec"
	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test_DetectSucceeded_ArtifactInStatusUpdate verifies that the DETECT behavior routes through
// TfPlanCommand, executes terraform plan, reads the plan file, and sends it as a
// base64-encoded artifact in the final status update.
func (suite *WorkerTestSuite) Test_DetectSucceeded_ArtifactInStatusUpdate() {
	planBytes := []byte("fake-plan-binary-data")
	planFuncCalled := false

	// Write the plan file when Plan() is called so that execute() can read it back.
	// The plan file path is always <workingDirectory>/plan.tfplan; we extract workingDirectory
	// from the context injected via runInfoContextKey.
	suite.tfMock.planFunc = func(ctx context.Context, opts ...tfexec.PlanOption) (bool, error) {
		planFuncCalled = true
		rci := ctx.Value(runInfoContextKey).(*RunContextInfo)
		return true, os.WriteFile(rci.artifactFilePath, planBytes, 0600)
	}

	suite.calls.fetch = mockValidRunDetailsFetchCall(DETECT.str(), "https://github.com/meshcloud/meshstack-hub.git", "modules/github/repository/buildingblock")

	updateCalls := make([]http.Request, 0)
	suite.calls.update = func(req *http.Request) *http.Response {
		updateCalls = append(updateCalls, *req)
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBuffer([]byte("{}"))),
			Header:     make(http.Header),
		}
	}

	suite.runWorker()

	require.GreaterOrEqual(suite.T(), len(updateCalls), 1)
	lastUpdate := updateCalls[len(updateCalls)-1]
	data, err := io.ReadAll(lastUpdate.Body)
	require.NoError(suite.T(), err)
	var update meshapi.RunStatusUpdateDTO
	require.NoError(suite.T(), json.Unmarshal(data, &update))

	assert.True(suite.T(), planFuncCalled, "expected DETECT behavior to invoke terraform plan")
	assert.Equal(suite.T(), SUCCEEDED.str(), *update.Status)
	require.NotEmpty(suite.T(), update.Artifact, "expected artifact to be set in status update")

	decoded, err := base64.StdEncoding.DecodeString(update.Artifact)
	require.NoError(suite.T(), err)
	assert.Equal(suite.T(), planBytes, decoded)

	require.NotNil(suite.T(), update.ChangesDetected, "expected changesDetected to be reported for a DETECT run")
	assert.True(suite.T(), *update.ChangesDetected, "planFunc returned changes present")
}

// Test_DetectSucceeded_NoChangesDetected verifies that when terraform plan reports no changes
// (Plan returns false), the DETECT status update carries changesDetected=false.
func (suite *WorkerTestSuite) Test_DetectSucceeded_NoChangesDetected() {
	planBytes := []byte("fake-plan-binary-data")
	suite.tfMock.planFunc = func(ctx context.Context, opts ...tfexec.PlanOption) (bool, error) {
		rci := ctx.Value(runInfoContextKey).(*RunContextInfo)
		return false, os.WriteFile(rci.artifactFilePath, planBytes, 0600)
	}

	suite.calls.fetch = mockValidRunDetailsFetchCall(DETECT.str(), "https://github.com/meshcloud/meshstack-hub.git", "modules/github/repository/buildingblock")

	updateCalls := make([]http.Request, 0)
	suite.calls.update = func(req *http.Request) *http.Response {
		updateCalls = append(updateCalls, *req)
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBuffer([]byte("{}"))),
			Header:     make(http.Header),
		}
	}

	suite.runWorker()

	require.GreaterOrEqual(suite.T(), len(updateCalls), 1)
	lastUpdate := updateCalls[len(updateCalls)-1]
	data, err := io.ReadAll(lastUpdate.Body)
	require.NoError(suite.T(), err)
	var update meshapi.RunStatusUpdateDTO
	require.NoError(suite.T(), json.Unmarshal(data, &update))

	assert.Equal(suite.T(), SUCCEEDED.str(), *update.Status)
	require.NotNil(suite.T(), update.ChangesDetected, "expected changesDetected to be reported for a DETECT run")
	assert.False(suite.T(), *update.ChangesDetected, "planFunc returned no changes")
}

// Test_ApplyWithPlanArtifact_DownloadsAndAppliesSavedPlan verifies that an APPLY run carrying a
// planArtifact link downloads the predecessor plan bytes, writes them to <wd>/plan.tfplan, and
// invokes terraform apply with a (DirOrPlan) option pointing at that saved plan instead of a plain
// re-plan+apply.
func (suite *WorkerTestSuite) Test_ApplyWithPlanArtifact_DownloadsAndAppliesSavedPlan() {
	savedPlanBytes := []byte("predecessor-saved-plan-binary")

	planArtifactHref := "http://localhost/api/meshobjects/meshbuildingblockruns/run-uuid/plan-artifact"
	suite.calls.fetch = mockApplyRunWithPlanArtifactFetchCall(
		"https://github.com/meshcloud/meshstack-hub.git",
		"modules/github/repository/buildingblock",
		planArtifactHref,
	)

	downloadCalled := false
	suite.calls.download = func(req *http.Request) *http.Response {
		downloadCalled = true
		assert.Equal(suite.T(), "application/octet-stream", req.Header.Get("Accept"))
		// the run-scoped bearer token from the fetched run must be used for the download
		assert.Equal(suite.T(), "Bearer test-mock-run-token-12345", req.Header.Get("Authorization"))
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBuffer(savedPlanBytes)),
			Header:     make(http.Header),
		}
	}

	applyOptCount := -1
	var planOnDisk []byte
	suite.tfMock.applyFunc = func(ctx context.Context, opts ...tfexec.ApplyOption) error {
		applyOptCount = len(opts)
		rci := ctx.Value(runInfoContextKey).(*RunContextInfo)
		planOnDisk, _ = os.ReadFile(rci.artifactFilePath)
		return nil
	}

	updateCalls := make([]http.Request, 0)
	suite.calls.update = func(req *http.Request) *http.Response {
		updateCalls = append(updateCalls, *req)
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBuffer([]byte("{}"))),
			Header:     make(http.Header),
		}
	}

	suite.runWorker()

	assert.True(suite.T(), downloadCalled, "expected the predecessor plan artifact to be downloaded")
	assert.Equal(suite.T(), 1, applyOptCount, "expected apply to be called with a single (DirOrPlan) option")
	assert.Equal(suite.T(), savedPlanBytes, planOnDisk, "expected the downloaded plan bytes to be written to plan.tfplan")

	require.GreaterOrEqual(suite.T(), len(updateCalls), 1)
	lastUpdate := updateCalls[len(updateCalls)-1]
	data, err := io.ReadAll(lastUpdate.Body)
	require.NoError(suite.T(), err)
	var update meshapi.RunStatusUpdateDTO
	require.NoError(suite.T(), json.Unmarshal(data, &update))
	assert.Equal(suite.T(), SUCCEEDED.str(), *update.Status)
}

// Test_ApplyWithoutPlanArtifact_PlainApply is the backward-compatibility regression: an APPLY run
// with NO planArtifact link must do a plain terraform apply (no download, no DirOrPlan option).
func (suite *WorkerTestSuite) Test_ApplyWithoutPlanArtifact_PlainApply() {
	suite.calls.fetch = mockValidRunDetailsFetchCall(APPLY.str(), "https://github.com/meshcloud/meshstack-hub.git", "modules/github/repository/buildingblock")

	downloadCalled := false
	suite.calls.download = func(req *http.Request) *http.Response {
		downloadCalled = true
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBuffer(nil)), Header: make(http.Header)}
	}

	applyOptCount := -1
	suite.tfMock.applyFunc = func(ctx context.Context, opts ...tfexec.ApplyOption) error {
		applyOptCount = len(opts)
		return nil
	}

	updateCalls := make([]http.Request, 0)
	suite.calls.update = func(req *http.Request) *http.Response {
		updateCalls = append(updateCalls, *req)
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBuffer([]byte("{}"))),
			Header:     make(http.Header),
		}
	}

	suite.runWorker()

	assert.False(suite.T(), downloadCalled, "plain apply must not download any plan artifact")
	assert.Equal(suite.T(), 0, applyOptCount, "plain apply must call terraform apply with no plan option")

	require.GreaterOrEqual(suite.T(), len(updateCalls), 1)
	lastUpdate := updateCalls[len(updateCalls)-1]
	data, err := io.ReadAll(lastUpdate.Body)
	require.NoError(suite.T(), err)
	var update meshapi.RunStatusUpdateDTO
	require.NoError(suite.T(), json.Unmarshal(data, &update))
	assert.Equal(suite.T(), SUCCEEDED.str(), *update.Status)
	assert.Nil(suite.T(), update.ChangesDetected, "APPLY runs must not report changesDetected")
}

// Test_ApplyWithPlanArtifact_DownloadFailureFailsRun verifies that when the planArtifact download
// returns a non-2xx (e.g. the artifact is gone), the run FAILS and terraform apply is never called.
func (suite *WorkerTestSuite) Test_ApplyWithPlanArtifact_DownloadFailureFailsRun() {
	planArtifactHref := "http://localhost/api/meshobjects/meshbuildingblockruns/run-uuid/plan-artifact"
	suite.calls.fetch = mockApplyRunWithPlanArtifactFetchCall(
		"https://github.com/meshcloud/meshstack-hub.git",
		"modules/github/repository/buildingblock",
		planArtifactHref,
	)

	suite.calls.download = func(req *http.Request) *http.Response {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(bytes.NewBuffer([]byte("not found"))),
			Header:     make(http.Header),
		}
	}

	applyCalled := false
	suite.tfMock.applyFunc = func(ctx context.Context, opts ...tfexec.ApplyOption) error {
		applyCalled = true
		return nil
	}

	updateCalls := make([]http.Request, 0)
	suite.calls.update = func(req *http.Request) *http.Response {
		updateCalls = append(updateCalls, *req)
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBuffer([]byte("{}"))),
			Header:     make(http.Header),
		}
	}

	suite.runWorker()

	assert.False(suite.T(), applyCalled, "terraform apply must not be called when the plan download fails")

	require.GreaterOrEqual(suite.T(), len(updateCalls), 1)
	lastUpdate := updateCalls[len(updateCalls)-1]
	data, err := io.ReadAll(lastUpdate.Body)
	require.NoError(suite.T(), err)
	var update meshapi.RunStatusUpdateDTO
	require.NoError(suite.T(), json.Unmarshal(data, &update))

	assert.Equal(suite.T(), FAILED.str(), *update.Status)
	executeTf := findStep(suite.T(), update, StepExecuteTf)
	assert.Equal(suite.T(), FAILED.str(), *executeTf.Status)
	require.NotNil(suite.T(), executeTf.UserMessage)
	assert.Contains(suite.T(), *executeTf.UserMessage, "previewed terraform plan")
}

// Test_DetectFailed_WhenPlanFileNotWritten verifies that the run fails when
// terraform plan "succeeds" but does not produce the expected plan file.
func (suite *WorkerTestSuite) Test_DetectFailed_WhenPlanFileNotWritten() {
	// planFunc succeeds without writing plan.tfplan, simulating a broken tf binary.
	planFuncCalled := false
	suite.tfMock.planFunc = func(ctx context.Context, opts ...tfexec.PlanOption) (bool, error) {
		planFuncCalled = true
		return true, nil
	}

	suite.calls.fetch = mockValidRunDetailsFetchCall(DETECT.str(), "https://github.com/meshcloud/meshstack-hub.git", "modules/github/repository/buildingblock")

	updateCalls := make([]http.Request, 0)
	suite.calls.update = func(req *http.Request) *http.Response {
		updateCalls = append(updateCalls, *req)
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBuffer([]byte("{}"))),
			Header:     make(http.Header),
		}
	}

	suite.runWorker()

	require.GreaterOrEqual(suite.T(), len(updateCalls), 1)
	lastUpdate := updateCalls[len(updateCalls)-1]
	data, err := io.ReadAll(lastUpdate.Body)
	require.NoError(suite.T(), err)
	var update meshapi.RunStatusUpdateDTO
	require.NoError(suite.T(), json.Unmarshal(data, &update))

	assert.True(suite.T(), planFuncCalled, "expected DETECT behavior to invoke terraform plan")
	assert.Equal(suite.T(), FAILED.str(), *update.Status)
	assert.Empty(suite.T(), update.Artifact)

	executeTf := findStep(suite.T(), update, StepExecuteTf)
	assert.Equal(suite.T(), FAILED.str(), *executeTf.Status)
	require.NotNil(suite.T(), executeTf.UserMessage)
	assert.Contains(suite.T(), *executeTf.UserMessage, "failed to read plan artifact")
}
