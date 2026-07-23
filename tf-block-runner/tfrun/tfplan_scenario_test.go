package tfrun

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"

	"github.com/hashicorp/terraform-exec/tfexec"
	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test_DetectSucceeded_UploadsArtifactViaEndpoint verifies that a DETECT run PUTs the plan bytes to
// the planArtifactUpload endpoint (correct method, URL, run-scoped auth, and payload) and ends SUCCEEDED.
func (suite *WorkerTestSuite) Test_DetectSucceeded_UploadsArtifactViaEndpoint() {
	planBytes := []byte("fake-plan-binary-data")
	uploadHref := "http://localhost/api/meshobjects/meshbuildingblockruns/run-uuid/plan-artifact"

	suite.tfMock.planFunc = func(ctx context.Context, opts ...tfexec.PlanOption) (bool, error) {
		rci := ctx.Value(runInfoContextKey).(*RunContextInfo)
		return true, os.WriteFile(rci.artifactFilePath, planBytes, 0600)
	}

	suite.calls.fetch = mockDetectRunWithUploadLinkFetchCall(
		"https://github.com/meshcloud/meshstack-hub.git",
		"modules/github/repository/buildingblock",
		uploadHref,
	)

	var uploadedBytes []byte
	uploadCalled := false
	suite.calls.upload = func(req *http.Request) *http.Response {
		uploadCalled = true
		assert.Equal(suite.T(), http.MethodPut, req.Method, "plan artifact must be uploaded via PUT")
		assert.Equal(suite.T(), uploadHref, req.URL.String(), "plan artifact must be uploaded to the planArtifactUpload link")
		// the run-scoped bearer token from the fetched run must be used for the upload
		assert.Equal(suite.T(), "Bearer test-mock-run-token-12345", req.Header.Get("Authorization"))
		var err error
		uploadedBytes, err = io.ReadAll(req.Body)
		require.NoError(suite.T(), err)
		return &http.Response{
			StatusCode: 204,
			Body:       io.NopCloser(bytes.NewBuffer(nil)),
			Header:     make(http.Header),
		}
	}

	updateBodies := make([][]byte, 0)
	suite.calls.update = func(req *http.Request) *http.Response {
		body, _ := io.ReadAll(req.Body)
		updateBodies = append(updateBodies, body)
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBuffer([]byte("{}"))),
			Header:     make(http.Header),
		}
	}

	suite.runWorker()

	assert.True(suite.T(), uploadCalled, "expected the plan artifact to be uploaded via the dedicated endpoint")
	assert.Equal(suite.T(), planBytes, uploadedBytes)

	require.GreaterOrEqual(suite.T(), len(updateBodies), 1)
	var lastUpdate meshapi.RunStatusUpdateDTO
	require.NoError(suite.T(), json.Unmarshal(updateBodies[len(updateBodies)-1], &lastUpdate))
	assert.Equal(suite.T(), SUCCEEDED.str(), *lastUpdate.Status)
}

// Test_DetectSucceeded_UploadFailureFailsRun verifies that when the plan-artifact upload returns a
// non-2xx, the run FAILS rather than reporting SUCCEEDED — the follow-up APPLY relies on the stored
// bytes, so a run must never declare success while the plan is missing from the backend.
func (suite *WorkerTestSuite) Test_DetectSucceeded_UploadFailureFailsRun() {
	planBytes := []byte("fake-plan-binary-data")
	uploadHref := "http://localhost/api/meshobjects/meshbuildingblockruns/run-uuid/plan-artifact"

	suite.tfMock.planFunc = func(ctx context.Context, opts ...tfexec.PlanOption) (bool, error) {
		rci := ctx.Value(runInfoContextKey).(*RunContextInfo)
		return true, os.WriteFile(rci.artifactFilePath, planBytes, 0600)
	}

	suite.calls.fetch = mockDetectRunWithUploadLinkFetchCall(
		"https://github.com/meshcloud/meshstack-hub.git",
		"modules/github/repository/buildingblock",
		uploadHref,
	)

	suite.calls.upload = func(req *http.Request) *http.Response {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(bytes.NewBuffer([]byte("upload broke"))),
			Header:     make(http.Header),
		}
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

	require.GreaterOrEqual(suite.T(), len(updateCalls), 1)
	lastUpdate := updateCalls[len(updateCalls)-1]
	data, err := io.ReadAll(lastUpdate.Body)
	require.NoError(suite.T(), err)
	var update meshapi.RunStatusUpdateDTO
	require.NoError(suite.T(), json.Unmarshal(data, &update))

	assert.Equal(suite.T(), FAILED.str(), *update.Status, "a failed plan upload must fail the run")
	executeTf := findStep(suite.T(), update, StepExecuteTf)
	assert.Equal(suite.T(), FAILED.str(), *executeTf.Status)
	require.NotNil(suite.T(), executeTf.UserMessage)
	assert.Contains(suite.T(), *executeTf.UserMessage, "upload plan artifact")
}

// Test_DetectFailed_WhenNoUploadUrl verifies that a DETECT run whose backend provided no
// planArtifactUpload link FAILS instead of reporting SUCCEEDED. Since the plan is no longer part of
// the status update, a missing upload URL would silently drop the plan and leave a follow-up APPLY
// with nothing to replay.
func (suite *WorkerTestSuite) Test_DetectFailed_WhenNoUploadUrl() {
	planBytes := []byte("fake-plan-binary-data")
	suite.tfMock.planFunc = func(ctx context.Context, opts ...tfexec.PlanOption) (bool, error) {
		rci := ctx.Value(runInfoContextKey).(*RunContextInfo)
		return true, os.WriteFile(rci.artifactFilePath, planBytes, 0600)
	}

	suite.calls.fetch = mockDetectRunWithoutUploadLinkFetchCall(
		"https://github.com/meshcloud/meshstack-hub.git",
		"modules/github/repository/buildingblock",
	)

	uploadCalled := false
	suite.calls.upload = func(req *http.Request) *http.Response {
		uploadCalled = true
		return &http.Response{
			StatusCode: 204,
			Body:       io.NopCloser(bytes.NewBuffer(nil)),
			Header:     make(http.Header),
		}
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

	assert.False(suite.T(), uploadCalled, "no upload must be attempted when the upload URL is missing")

	require.GreaterOrEqual(suite.T(), len(updateCalls), 1)
	lastUpdate := updateCalls[len(updateCalls)-1]
	data, err := io.ReadAll(lastUpdate.Body)
	require.NoError(suite.T(), err)
	var update meshapi.RunStatusUpdateDTO
	require.NoError(suite.T(), json.Unmarshal(data, &update))

	assert.Equal(suite.T(), FAILED.str(), *update.Status, "a missing upload URL must fail the run")
	executeTf := findStep(suite.T(), update, StepExecuteTf)
	assert.Equal(suite.T(), FAILED.str(), *executeTf.Status)
	require.NotNil(suite.T(), executeTf.UserMessage)
	assert.Contains(suite.T(), *executeTf.UserMessage, "upload URL")
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

	executeTf := findStep(suite.T(), update, StepExecuteTf)
	assert.Equal(suite.T(), FAILED.str(), *executeTf.Status)
	require.NotNil(suite.T(), executeTf.UserMessage)
	assert.Contains(suite.T(), *executeTf.UserMessage, "failed to read plan artifact")
}
