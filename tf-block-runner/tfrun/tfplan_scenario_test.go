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
