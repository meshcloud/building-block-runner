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
)

// Test_DetectSucceeded_ArtifactInStatusUpdate verifies that the DETECT behavior routes through
// TfPlanCommand, executes terraform plan, reads the plan file, and sends it as a
// base64-encoded artifact in the final status update.
func (suite *WorkerTestSuite) Test_DetectSucceeded_ArtifactInStatusUpdate() {
	planBytes := []byte("fake-plan-binary-data")
	planFuncCalled := false

	// Write the plan file when Plan() is called so that execute() can read it back.
	// The plan file path is always <workingDirectory>/plan.tfplan; the mock exposes it via
	// artifactPath() using the working dir captured in GetTF.
	suite.tfMock.planFunc = func(ctx context.Context, opts ...tfexec.PlanOption) (bool, error) {
		planFuncCalled = true
		return true, os.WriteFile(suite.tfMock.artifactPath(), planBytes, 0600)
	}

	suite.calls.fetch = mockValidRunDetailsFetchCall(DETECT.str(), suite.repo.Path, suite.repoPath)

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

	suite.Require().GreaterOrEqual(len(updateCalls), 1)
	lastUpdate := updateCalls[len(updateCalls)-1]
	data, err := io.ReadAll(lastUpdate.Body)
	suite.Require().NoError(err)
	var update meshapi.RunStatusUpdateDTO
	suite.Require().NoError(json.Unmarshal(data, &update))

	suite.True(planFuncCalled, "expected DETECT behavior to invoke terraform plan")
	suite.Equal(SUCCEEDED.str(), *update.Status)
	suite.Require().NotEmpty(update.Artifact, "expected artifact to be set in status update")

	decoded, err := base64.StdEncoding.DecodeString(update.Artifact)
	suite.Require().NoError(err)
	suite.Equal(planBytes, decoded)
}

// Test_ApplyWithPlanArtifact_DownloadsAndAppliesSavedPlan verifies that an APPLY run carrying a
// planArtifact link downloads the predecessor plan bytes, writes them to <wd>/plan.tfplan, and
// invokes terraform apply with a (DirOrPlan) option pointing at that saved plan instead of a plain
// re-plan+apply.
func (suite *WorkerTestSuite) Test_ApplyWithPlanArtifact_DownloadsAndAppliesSavedPlan() {
	savedPlanBytes := []byte("predecessor-saved-plan-binary")

	planArtifactHref := "http://localhost/api/meshobjects/meshbuildingblockruns/run-uuid/plan-artifact"
	suite.calls.fetch = mockApplyRunWithPlanArtifactFetchCall(
		suite.repo.Path,
		suite.repoPath,
		planArtifactHref,
	)

	downloadCalled := false
	suite.calls.download = func(req *http.Request) *http.Response {
		downloadCalled = true
		suite.Equal("application/octet-stream", req.Header.Get("Accept"))
		// the run-scoped bearer token from the fetched run must be used for the download
		suite.Equal("Bearer test-mock-run-token-12345", req.Header.Get("Authorization"))
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
		planOnDisk, _ = os.ReadFile(suite.tfMock.artifactPath())
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

	suite.True(downloadCalled, "expected the predecessor plan artifact to be downloaded")
	suite.Equal(1, applyOptCount, "expected apply to be called with a single (DirOrPlan) option")
	suite.Equal(savedPlanBytes, planOnDisk, "expected the downloaded plan bytes to be written to plan.tfplan")

	suite.Require().GreaterOrEqual(len(updateCalls), 1)
	lastUpdate := updateCalls[len(updateCalls)-1]
	data, err := io.ReadAll(lastUpdate.Body)
	suite.Require().NoError(err)
	var update meshapi.RunStatusUpdateDTO
	suite.Require().NoError(json.Unmarshal(data, &update))
	suite.Equal(SUCCEEDED.str(), *update.Status)
}

// Test_ApplyWithoutPlanArtifact_PlainApply is the backward-compatibility regression: an APPLY run
// with NO planArtifact link must do a plain terraform apply (no download, no DirOrPlan option).
func (suite *WorkerTestSuite) Test_ApplyWithoutPlanArtifact_PlainApply() {
	suite.calls.fetch = mockValidRunDetailsFetchCall(APPLY.str(), suite.repo.Path, suite.repoPath)

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

	suite.False(downloadCalled, "plain apply must not download any plan artifact")
	suite.Equal(0, applyOptCount, "plain apply must call terraform apply with no plan option")

	suite.Require().GreaterOrEqual(len(updateCalls), 1)
	lastUpdate := updateCalls[len(updateCalls)-1]
	data, err := io.ReadAll(lastUpdate.Body)
	suite.Require().NoError(err)
	var update meshapi.RunStatusUpdateDTO
	suite.Require().NoError(json.Unmarshal(data, &update))
	suite.Equal(SUCCEEDED.str(), *update.Status)
}

// Test_ApplyWithPlanArtifact_DownloadFailureFailsRun verifies that when the planArtifact download
// returns a non-2xx (e.g. the artifact is gone), the run FAILS and terraform apply is never called.
func (suite *WorkerTestSuite) Test_ApplyWithPlanArtifact_DownloadFailureFailsRun() {
	planArtifactHref := "http://localhost/api/meshobjects/meshbuildingblockruns/run-uuid/plan-artifact"
	suite.calls.fetch = mockApplyRunWithPlanArtifactFetchCall(
		suite.repo.Path,
		suite.repoPath,
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

	suite.False(applyCalled, "terraform apply must not be called when the plan download fails")

	suite.Require().GreaterOrEqual(len(updateCalls), 1)
	lastUpdate := updateCalls[len(updateCalls)-1]
	data, err := io.ReadAll(lastUpdate.Body)
	suite.Require().NoError(err)
	var update meshapi.RunStatusUpdateDTO
	suite.Require().NoError(json.Unmarshal(data, &update))

	suite.Equal(FAILED.str(), *update.Status)
	executeTf := findStep(suite.T(), update, StepExecuteTf)
	suite.Equal(FAILED.str(), *executeTf.Status)
	suite.Require().NotNil(executeTf.UserMessage)
	suite.Contains(*executeTf.UserMessage, "previewed terraform plan")
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

	suite.calls.fetch = mockValidRunDetailsFetchCall(DETECT.str(), suite.repo.Path, suite.repoPath)

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

	suite.Require().GreaterOrEqual(len(updateCalls), 1)
	lastUpdate := updateCalls[len(updateCalls)-1]
	data, err := io.ReadAll(lastUpdate.Body)
	suite.Require().NoError(err)
	var update meshapi.RunStatusUpdateDTO
	suite.Require().NoError(json.Unmarshal(data, &update))

	suite.True(planFuncCalled, "expected DETECT behavior to invoke terraform plan")
	suite.Equal(FAILED.str(), *update.Status)
	suite.Empty(update.Artifact)

	executeTf := findStep(suite.T(), update, StepExecuteTf)
	suite.Equal(FAILED.str(), *executeTf.Status)
	suite.Require().NotNil(executeTf.UserMessage)
	suite.Contains(*executeTf.UserMessage, "failed to read plan artifact")
}
