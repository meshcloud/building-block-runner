package tfrun

// CP7 (PLAN_DETAIL_01_tf_characterization_tests.md §9): async runs. Async terraform runs hand
// execution over to an external pipeline, so they collapse to a single "trigger"/"Prepare Run" step
// and — the D9 pin — report a final IN_PROGRESS (not SUCCEEDED) once the internal run succeeds, since
// the real work continues out-of-band. Async DESTROY additionally runs an apply before the destroy
// (tfdestroy.go:161-169) to propagate the run object into the pipeline. Async failures still map to
// FAILED (the IN_PROGRESS mapping is SUCCEEDED-only).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"

	"github.com/hashicorp/terraform-exec/tfexec"
	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
)

// captureRegister installs a register handler recording the initial registration DTO (which carries
// the run's initRunSteps shape) and acking 200.
func (suite *WorkerTestSuite) captureRegister(into *meshapi.RunStatusUpdateDTO) {
	suite.calls.register = func(req *http.Request) *http.Response {
		data, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(data, into)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBuffer([]byte("{}"))), Header: make(http.Header)}
	}
}

func (suite *WorkerTestSuite) Test_AsyncApply_SingleTriggerStep_FinalInProgress() {
	suite.calls.fetch = runDetailsFetchCall(withBehavior(APPLY.str()), withRepo(suite.repo.Path, suite.repoPath), withAsync())

	var registerDTO meshapi.RunStatusUpdateDTO
	suite.captureRegister(&registerDTO)
	updateCalls := suite.collectUpdatesWorker()

	suite.runWorker()

	// Registration DTO carries the single async "trigger" step, never the six sync steps.
	suite.Require().Len(registerDTO.Steps, 1)
	suite.Equal(StepTrigger, registerDTO.Steps[0].Id)

	suite.Require().GreaterOrEqual(len(*updateCalls), 1)
	// Decode each body exactly once (the request Body is a single-use stream).
	decoded := make([]meshapi.RunStatusUpdateDTO, len(*updateCalls))
	for i, req := range *updateCalls {
		decoded[i] = decodeUpdate(suite.T(), req)
	}

	// No intermediate update ever advances past the trigger step (nextStep is a no-op for async).
	for _, u := range decoded {
		for _, s := range u.Steps {
			suite.Equal(StepTrigger, s.Id, "async runs only ever report the trigger step")
		}
	}

	final := decoded[len(decoded)-1]
	suite.Equal(IN_PROGRESS.str(), *final.Status, "async success maps SUCCEEDED->IN_PROGRESS")
}

func (suite *WorkerTestSuite) Test_AsyncDestroy_AppliesBeforeDestroy() {
	var order []string
	suite.tfMock.applyFunc = func(ctx context.Context, opts ...tfexec.ApplyOption) error {
		order = append(order, "apply")
		return nil
	}
	suite.tfMock.destroyFunc = func(ctx context.Context, opts ...tfexec.DestroyOption) error {
		order = append(order, "destroy")
		return nil
	}

	suite.calls.fetch = runDetailsFetchCall(withBehavior(DESTROY.str()), withRepo(suite.repo.Path, suite.repoPath), withAsync())
	updateCalls := suite.collectUpdatesWorker()

	suite.runWorker()

	suite.Equal([]string{"apply", "destroy"}, order, "async destroy runs apply first to propagate the run id (tfdestroy.go:161-169)")

	final := decodeUpdate(suite.T(), (*updateCalls)[len(*updateCalls)-1])
	suite.Equal(IN_PROGRESS.str(), *final.Status)
}

func (suite *WorkerTestSuite) Test_AsyncDetect_FinalInProgress_ArtifactAttached() {
	planBytes := []byte("async-detect-plan")
	suite.tfMock.planFunc = func(ctx context.Context, opts ...tfexec.PlanOption) (bool, error) {
		return true, os.WriteFile(suite.tfMock.artifactPath(), planBytes, 0600)
	}

	suite.calls.fetch = runDetailsFetchCall(withBehavior(DETECT.str()), withRepo(suite.repo.Path, suite.repoPath), withAsync())
	updateCalls := suite.collectUpdatesWorker()

	suite.runWorker()

	final := decodeUpdate(suite.T(), (*updateCalls)[len(*updateCalls)-1])
	suite.Equal(IN_PROGRESS.str(), *final.Status)
	suite.NotEmpty(final.Artifact, "async DETECT still attaches the plan artifact")
}

func (suite *WorkerTestSuite) Test_AsyncApply_TfFailure_FinalFailed() {
	suite.tfMock.applyFunc = func(ctx context.Context, opts ...tfexec.ApplyOption) error {
		return errors.New("async apply blew up")
	}

	suite.calls.fetch = runDetailsFetchCall(withBehavior(APPLY.str()), withRepo(suite.repo.Path, suite.repoPath), withAsync())
	updateCalls := suite.collectUpdatesWorker()

	suite.runWorker()

	final := decodeUpdate(suite.T(), (*updateCalls)[len(*updateCalls)-1])
	suite.Equal(FAILED.str(), *final.Status, "async failure is not remapped to IN_PROGRESS")
}

// collectUpdatesWorker mirrors SingleRunWorkerTestSuite.collectUpdates for the polling suite.
func (suite *WorkerTestSuite) collectUpdatesWorker() *[]http.Request {
	updateCalls := make([]http.Request, 0)
	suite.calls.update = func(req *http.Request) *http.Response {
		updateCalls = append(updateCalls, *req)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBuffer([]byte("{}"))), Header: make(http.Header)}
	}
	return &updateCalls
}

// --- single-run async twin -------------------------------------------------------------------

func (suite *SingleRunWorkerTestSuite) Test_ExecuteRun_AsyncApply_FinalInProgress() {
	w := suite.newWorker("single-run-token-async")
	run := suite.buildRun(withBehavior(APPLY.str()), withRepo(suite.repo.Path, suite.repoPath), withAsync())

	updateCalls := suite.collectUpdates()

	suite.Require().NoError(w.ExecuteRun(run))

	suite.Require().GreaterOrEqual(len(*updateCalls), 1)
	final := decodeUpdate(suite.T(), (*updateCalls)[len(*updateCalls)-1])
	suite.Equal(IN_PROGRESS.str(), *final.Status, "single-run async success also maps to IN_PROGRESS (singlerunworker.go:160-165)")
	for _, s := range final.Steps {
		suite.Equal(StepTrigger, s.Id)
	}
}
