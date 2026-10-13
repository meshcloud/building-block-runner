package tfrun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/config/xtime"
	internalhttp "github.com/meshcloud/building-block-runner/internal/http"
	"github.com/meshcloud/building-block-runner/internal/http/meshapi"
)

type WorkerTestSuite struct {
	suite.Suite
	cfg    config.Config
	w      *Worker
	run    meshapi.MeshRunnerRun
	calls  MockRunApiCalls
	tfBin  *TfBinaries
	tfMock *MockedTfFacade
}

type MockRunApiCalls struct {
	register, update, download, upload func(*http.Request) *http.Response
}

func noopCall(req *http.Request) *http.Response {
	return &http.Response{
		StatusCode: 200,
	}
}

func (suite *WorkerTestSuite) scenarioClientBehavior(req *http.Request) *http.Response {
	switch {
	case req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/plan-artifact"):
		return suite.calls.download(req)
	case req.Method == http.MethodPut && strings.Contains(req.URL.Path, "/plan-artifact"):
		return suite.calls.upload(req)
	case req.Method == http.MethodPatch && strings.Contains(req.URL.Path, "/status/source"):
		return suite.calls.update(req)
	case req.Method == http.MethodPost && strings.Contains(req.URL.Path, "/status/source"):
		return suite.calls.register(req)
	default:
		suite.Failf("unexpected request", "%s %s", req.Method, req.URL)
		return &http.Response{StatusCode: http.StatusNotFound}
	}
}

// For tests that download tofu or terraform or clone from GitHub, which takes minutes.
func skipUnlessAcc(t *testing.T) {
	t.Helper()
	if os.Getenv("BUILDING_BLOCK_RUNNER_TEST_ACC") != "1" {
		t.Skip("acceptance tests are off. Run them with BUILDING_BLOCK_RUNNER_TEST_ACC=1 go test ./tf-block-runner/tfrun/ -run TestAcc")
	}
}

func TestAccWorkerSuite(t *testing.T) {
	skipUnlessAcc(t)
	suite.Run(t, new(WorkerTestSuite))
}

func (suite *WorkerTestSuite) SetupSuite() {
	testTfInstallDir, err := os.MkdirTemp(os.TempDir(), "workerScenario-tf-")
	if err != nil {
		panic(err)
	}

	tmpWd, err := os.MkdirTemp(os.TempDir(), "workerScenario-wd-")
	if err != nil {
		panic(err)
	}

	suite.cfg.Uuid = testRunnerUuid
	suite.cfg.Tf.TfParentWorkingDir = tmpWd
	suite.cfg.Tf.InitTimeout = xtime.Minutes(10)
	suite.cfg.Tf.WsTimeout = xtime.Minutes(10)

	suite.tfMock = &MockedTfFacade{}
	suite.tfMock.initMockFuncs()
	suite.tfBin, err = ForTestNewTfBin(testTfInstallDir, io.Discard, suite.tfMock)
	if err != nil {
		panic(err)
	}
}

func (suite *WorkerTestSuite) TearDownSuite() {
	os.RemoveAll(suite.tfBin.dir)
	os.RemoveAll(suite.cfg.Tf.TfParentWorkingDir)
}

func (suite *WorkerTestSuite) SetupTest() {
	suite.calls = MockRunApiCalls{
		register: noopCall,
		update:   noopCall,
		download: noopCall,
		upload:   noopCall,
	}

	suite.tfMock.initMockFuncs()

	suite.w = &Worker{
		cfg:                  suite.cfg,
		tfBinaries:           suite.tfBin,
		log:                  log.New(io.Discard, "", log.LstdFlags),
		statusUpdateInterval: time.Second * 10,
		statusRequestTimeout: time.Second,
		finalStatusRetry:     retrySchedule{initialDelay: time.Millisecond, maxDelay: time.Millisecond, attemptTimeout: time.Second, giveUpAfter: time.Second},
	}
}

func (suite *WorkerTestSuite) runWorker() {
	client := internalhttp.Client{Client: &http.Client{Transport: testRoundTripper(suite.scenarioClientBehavior)}, UserAgent: testUserAgent}
	suite.w.runApi = newTestRunApi(client, suite.run)
	run, err := newRun(suite.run, false)
	require.NoError(suite.T(), err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(suite.T(), suite.w.ExecuteRun(ctx, run))
}

// findStep returns the step with the given ID from a status update.
// It immediately fails the test if no matching step is found.
func findStep(t testing.TB, update meshapi.MeshRunnerRunStatusUpdate, stepId string) meshapi.MeshRunnerRunStepStatusUpdate {
	t.Helper()
	if s := findStepOrNil(update, stepId); s != nil {
		return *s
	}
	ids := make([]string, len(update.Steps))
	for i, s := range update.Steps {
		ids[i] = s.Id
	}
	t.Fatalf("step %q not found; available step IDs: %v", stepId, ids)
	return meshapi.MeshRunnerRunStepStatusUpdate{}
}

// findStepOrNil returns the step with the given ID, or nil if not present.
// Prefer findStep in tests that require the step to exist.
func findStepOrNil(update meshapi.MeshRunnerRunStatusUpdate, stepId string) *meshapi.MeshRunnerRunStepStatusUpdate {
	for i := range update.Steps {
		if update.Steps[i].Id == stepId {
			return &update.Steps[i]
		}
	}
	return nil
}

func (suite *WorkerTestSuite) Test_MissingAuth() {
	suite.run = validRun(APPLY.str(), "https://github.com/meshcloud/does-not-exist.git", "")

	updateCalls := make([]http.Request, 0)
	suite.calls.update = func(req *http.Request) *http.Response {
		updateCalls = append(updateCalls, *req)
		return noopCall(req)
	}

	// execute worker
	suite.runWorker()

	// assertions
	assert.Equal(suite.T(), 1, len(updateCalls))

	data, _ := io.ReadAll(updateCalls[0].Body)
	var update meshapi.MeshRunnerRunStatusUpdate
	json.Unmarshal(data, &update)

	assert.Equal(suite.T(), FAILED.str(), *update.Status)
	assert.Equal(suite.T(), 6, len(update.Steps))
	for i, step := range update.Steps {
		assert.Equal(suite.T(), FAILED.str(), *step.Status)
		if i == 0 {
			assert.Contains(suite.T(), *step.SystemMessage, "copy sources from")
			// UserMessage is now populated with the actual error to improve panel visibility
			assert.NotNil(suite.T(), step.UserMessage)
		} else {
			assert.Equal(suite.T(), "Aborted due to failure in an earlier step", *step.SystemMessage)
			assert.Nil(suite.T(), step.UserMessage)
		}
	}
}

func (suite *WorkerTestSuite) Test_ApplySucceeded() {
	suite.run = validRun(APPLY.str(), "https://github.com/meshcloud/meshstack-hub.git", "modules/github/repository/buildingblock")

	updateCalls := make([]http.Request, 0)
	suite.calls.update = func(req *http.Request) *http.Response {
		updateCalls = append(updateCalls, *req)
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBuffer([]byte("{}"))),
			Header:     make(http.Header),
		}
	}

	// execute worker
	suite.runWorker()

	// assertions - check for at least one update call
	assert.GreaterOrEqual(suite.T(), len(updateCalls), 1)

	// Check the final update call
	lastUpdate := updateCalls[len(updateCalls)-1]
	data, _ := io.ReadAll(lastUpdate.Body)
	var update meshapi.MeshRunnerRunStatusUpdate
	json.Unmarshal(data, &update)

	assert.Equal(suite.T(), SUCCEEDED.str(), *update.Status)
	for _, step := range update.Steps {
		assert.Equal(suite.T(), SUCCEEDED.str(), *step.Status)
		assert.Nil(suite.T(), step.UserMessage)
	}
	assert.Nil(suite.T(), update.Summary)
}

func (suite *WorkerTestSuite) Test_RegistrationConflict_ContinuesExecution() {
	// Regression test for Kubernetes pod retries:
	// When a pod is restarted (e.g. due to node pressure), the replacement pod's Register()
	// call receives 409 Conflict because the source was already registered by the previous pod.
	// The runner must treat 409 as idempotent and continue executing — it must NEVER report
	// PENDING status to the API (which would cause a 500 from the coordinator's state machine).

	suite.run = validRun(APPLY.str(), "https://github.com/meshcloud/meshstack-hub.git", "modules/github/repository/buildingblock")

	// Simulate a 409 from the registration endpoint (source already registered by previous pod)
	suite.calls.register = func(_ *http.Request) *http.Response {
		return &http.Response{
			StatusCode: http.StatusConflict,
			Body:       io.NopCloser(bytes.NewBuffer([]byte("conflict"))),
			Header:     make(http.Header),
		}
	}

	type capturedUpdate struct {
		req    http.Request
		status string
	}
	var mu sync.Mutex
	capturedUpdates := make([]capturedUpdate, 0)

	suite.calls.update = func(req *http.Request) *http.Response {
		data, _ := io.ReadAll(req.Body)
		// Restore body for later reads
		req.Body = io.NopCloser(bytes.NewBuffer(data))

		var update meshapi.MeshRunnerRunStatusUpdate
		json.Unmarshal(data, &update)

		statusStr := ""
		if update.Status != nil {
			statusStr = *update.Status
		}

		mu.Lock()
		capturedUpdates = append(capturedUpdates, capturedUpdate{req: *req, status: statusStr})
		mu.Unlock()

		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBuffer([]byte("{}"))),
			Header:     make(http.Header),
		}
	}

	suite.runWorker()

	// At least one status update must have been sent
	assert.GreaterOrEqual(suite.T(), len(capturedUpdates), 1, "expected at least one status update")

	// PENDING must NEVER appear in any update — it is a coordinator-only status and
	// sending it would cause the coordinator's state machine to reject it with a 500
	for _, u := range capturedUpdates {
		assert.NotEqual(suite.T(), PENDING.str(), u.status,
			"runner must never report PENDING status to the API (coordinator rejects it)")
	}

	// Final update must indicate successful execution
	finalStatus := capturedUpdates[len(capturedUpdates)-1].status
	assert.Equal(suite.T(), SUCCEEDED.str(), finalStatus,
		"run should complete successfully even when registration returns 409")
}

// tofuShutdownAfterInterrupt stands in for tofu after SIGINT: it stops the operation, saves the partial
// state and releases the state lock before it exits.
const tofuShutdownAfterInterrupt = 50 * time.Millisecond

// abortOnceApplyStarted answers every status update like meshStack does after a user aborted the run,
// but only once apply runs, so the abort reaches a running tofu and not an earlier step.
func (suite *WorkerTestSuite) abortOnceApplyStarted() (tofuExited *time.Time, receivedStatuses *[]string, finalStatusAnswer *int) {
	tofuExited, receivedStatuses, finalStatusAnswer = new(time.Time), new([]string), new(int)
	*finalStatusAnswer = http.StatusOK
	var applyStarted atomic.Bool

	suite.tfMock.applyFunc = func(ctx context.Context, opts ...tfexec.ApplyOption) error {
		applyStarted.Store(true)
		<-ctx.Done()
		time.Sleep(tofuShutdownAfterInterrupt)
		*tofuExited = time.Now()
		return ctx.Err()
	}
	suite.calls.update = func(req *http.Request) *http.Response {
		data, _ := io.ReadAll(req.Body)
		var update meshapi.MeshRunnerRunStatusUpdate
		json.Unmarshal(data, &update)
		*receivedStatuses = append(*receivedStatuses, *update.Status)
		switch {
		case *update.Status == ABORTED.str():
			assert.False(suite.T(), tofuExited.IsZero(), "ABORTED must be sent only after tofu has exited")
			return &http.Response{StatusCode: *finalStatusAnswer, Body: io.NopCloser(bytes.NewBufferString("{}")), Header: make(http.Header)}
		case applyStarted.Load():
			return mockUpdateCallWithAbortResponse()(req)
		default:
			return noopCall(req)
		}
	}
	return tofuExited, receivedStatuses, finalStatusAnswer
}

func (suite *WorkerTestSuite) Test_AbortedApply_SendsABORTEDOnceTofuHasExited() {
	suite.w.statusUpdateInterval = 10 * time.Millisecond
	suite.run = validRun(APPLY.str(), "https://github.com/meshcloud/meshstack-hub.git", "modules/github/repository/buildingblock")
	tofuExited, receivedStatuses, _ := suite.abortOnceApplyStarted()

	suite.runWorker()

	assert.False(suite.T(), tofuExited.IsZero())
	assert.Equal(suite.T(), ABORTED.str(), (*receivedStatuses)[len(*receivedStatuses)-1])
	assert.Equal(suite.T(), 1, countOf(*receivedStatuses, ABORTED.str()))
}

func (suite *WorkerTestSuite) Test_AbortedApplyOnMeshStackThatEndedTheRunItself_LogsTheRefusalWithoutError() {
	suite.w.statusUpdateInterval = 10 * time.Millisecond
	var workerLog bytes.Buffer
	suite.w.log = log.New(&workerLog, "", 0)
	suite.run = validRun(APPLY.str(), "https://github.com/meshcloud/meshstack-hub.git", "modules/github/repository/buildingblock")
	_, receivedStatuses, finalStatusAnswer := suite.abortOnceApplyStarted()
	*finalStatusAnswer = http.StatusUnauthorized

	suite.runWorker()

	assert.Equal(suite.T(), 1, countOf(*receivedStatuses, ABORTED.str()))
	assert.Contains(suite.T(), workerLog.String(), "meshStack refused the ABORTED status")
	assert.NotContains(suite.T(), workerLog.String(), "ERROR")
}

func countOf(values []string, value string) int {
	count := 0
	for _, v := range values {
		if v == value {
			count++
		}
	}
	return count
}

func (suite *WorkerTestSuite) Test_ApplyTfFailure() {
	suite.run = validRun(APPLY.str(), "https://github.com/meshcloud/meshstack-hub.git", "modules/github/repository/buildingblock")

	updateCalls := make([]http.Request, 0)
	suite.calls.update = func(req *http.Request) *http.Response {
		updateCalls = append(updateCalls, *req)
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBuffer([]byte("{}"))),
			Header:     make(http.Header),
		}
	}

	// make tf apply fail
	suite.tfMock.applyFunc = func(ctx context.Context, opts ...tfexec.ApplyOption) error {
		suite.tfMock.stdOut.Write([]byte("apply in progress\n"))
		suite.tfMock.stdErr.Write([]byte("failure\n"))
		return errors.New("test error")
	}

	// execute worker
	suite.runWorker()

	// assertions - now we expect multiple update calls due to error logging
	assert.GreaterOrEqual(suite.T(), len(updateCalls), 1)

	// Check the final update call
	lastUpdate := updateCalls[len(updateCalls)-1]
	data, _ := io.ReadAll(lastUpdate.Body)
	var update meshapi.MeshRunnerRunStatusUpdate
	json.Unmarshal(data, &update)

	assert.Equal(suite.T(), FAILED.str(), *update.Status)
	assert.Equal(suite.T(), 6, len(update.Steps))

	sources := findStep(suite.T(), update, StepSources)
	assert.Equal(suite.T(), SUCCEEDED.str(), *sources.Status)
	assert.Nil(suite.T(), sources.UserMessage)

	inputStep := findStep(suite.T(), update, StepInput)
	assert.Equal(suite.T(), SUCCEEDED.str(), *inputStep.Status)
	assert.Nil(suite.T(), inputStep.UserMessage)

	executeTf := findStep(suite.T(), update, StepExecuteTf)
	assert.Equal(suite.T(), FAILED.str(), *executeTf.Status)
	// UserMessage is now set to the error text for better panel visibility
	assert.NotNil(suite.T(), executeTf.UserMessage)
	assert.Equal(suite.T(), "test error", *executeTf.UserMessage)
	assert.Equal(suite.T(), "No plan artifact linked to this run; running a fresh terraform apply.\napply in progress\nfailure\ntest error\n", *executeTf.SystemMessage) // includes error message

	outputStep := findStep(suite.T(), update, StepOutput)
	assert.Equal(suite.T(), FAILED.str(), *outputStep.Status)
	assert.Nil(suite.T(), outputStep.UserMessage)
	assert.Equal(suite.T(), "Aborted due to failure in an earlier step", *outputStep.SystemMessage)
}

func (suite *WorkerTestSuite) Test_DestroySucceeded() {
	suite.run = validRun(DESTROY.str(), "https://github.com/meshcloud/meshstack-hub.git", "modules/github/repository/buildingblock")

	var destroyOpts []tfexec.DestroyOption
	suite.tfMock.destroyFunc = func(ctx context.Context, opts ...tfexec.DestroyOption) error {
		destroyOpts = opts
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

	// execute worker
	suite.runWorker()

	// assertions - check for at least one update call
	assert.GreaterOrEqual(suite.T(), len(updateCalls), 1)

	// Check the final update call
	lastUpdate := updateCalls[len(updateCalls)-1]
	data, _ := io.ReadAll(lastUpdate.Body)
	var update meshapi.MeshRunnerRunStatusUpdate
	json.Unmarshal(data, &update)

	assert.Equal(suite.T(), SUCCEEDED.str(), *update.Status)
	for _, step := range update.Steps {
		assert.Equal(suite.T(), SUCCEEDED.str(), *step.Status)
		assert.Nil(suite.T(), step.UserMessage)
	}
	assert.Nil(suite.T(), update.Summary)
	assert.Equal(suite.T(), []tfexec.DestroyOption{stateLockTimeout()}, destroyOpts, "a Terraform destroy gets no flag but the lock timeout")
}

func (suite *WorkerTestSuite) Test_DestroyTfFailure() {
	suite.run = validRun(DESTROY.str(), "https://github.com/meshcloud/meshstack-hub.git", "modules/github/repository/buildingblock")

	updateCalls := make([]http.Request, 0)
	suite.calls.update = func(req *http.Request) *http.Response {
		updateCalls = append(updateCalls, *req)
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBuffer([]byte("{}"))),
			Header:     make(http.Header),
		}
	}

	// make tf apply fail
	suite.tfMock.destroyFunc = func(ctx context.Context, opts ...tfexec.DestroyOption) error {
		suite.tfMock.stdOut.Write([]byte("destroy in progress\n"))
		suite.tfMock.stdErr.Write([]byte("failure\n"))
		return errors.New("test error")
	}

	// execute worker
	suite.runWorker()

	// assertions - now we expect multiple update calls due to error logging
	assert.GreaterOrEqual(suite.T(), len(updateCalls), 1)

	// Check the final update call
	lastUpdate := updateCalls[len(updateCalls)-1]
	data, _ := io.ReadAll(lastUpdate.Body)
	var update meshapi.MeshRunnerRunStatusUpdate
	json.Unmarshal(data, &update)

	assert.Equal(suite.T(), FAILED.str(), *update.Status)
	assert.Equal(suite.T(), 6, len(update.Steps))

	sources := findStep(suite.T(), update, StepSources)
	assert.Equal(suite.T(), SUCCEEDED.str(), *sources.Status)
	assert.Nil(suite.T(), sources.UserMessage)

	inputStep := findStep(suite.T(), update, StepInput)
	assert.Equal(suite.T(), SUCCEEDED.str(), *inputStep.Status)
	assert.Nil(suite.T(), inputStep.UserMessage)

	executeTf := findStep(suite.T(), update, StepExecuteTf)
	assert.Equal(suite.T(), FAILED.str(), *executeTf.Status)
	// UserMessage is now set to the error text for better panel visibility
	assert.NotNil(suite.T(), executeTf.UserMessage)
	assert.Equal(suite.T(), "test error", *executeTf.UserMessage)
	assert.Equal(suite.T(), "destroy in progress\nfailure\ntest error\n", *executeTf.SystemMessage) // includes error message

	outputStep := findStep(suite.T(), update, StepOutput)
	assert.Equal(suite.T(), FAILED.str(), *outputStep.Status)
	assert.Nil(suite.T(), outputStep.UserMessage)
	assert.Equal(suite.T(), "Aborted due to failure in an earlier step", *outputStep.SystemMessage)
}

func (suite *WorkerTestSuite) Test_FinalStatusIsSentWhenAHeartbeatGetsNoAnswer() {
	suite.w.statusUpdateInterval = 5 * time.Millisecond
	suite.w.statusRequestTimeout = 50 * time.Millisecond
	suite.run = validRun(DESTROY.str(), "https://github.com/meshcloud/meshstack-hub.git", "modules/github/repository/buildingblock")

	var workDone, finalStatusSent time.Time
	suite.calls.update = func(req *http.Request) *http.Response {
		data, _ := io.ReadAll(req.Body)
		var update meshapi.MeshRunnerRunStatusUpdate
		json.Unmarshal(data, &update)
		if *update.Status == FAILED.str() {
			finalStatusSent = time.Now()
		} else {
			select {
			case <-req.Context().Done():
			case <-time.After(10 * time.Second):
			}
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBuffer([]byte("{}"))),
			Header:     make(http.Header),
		}
	}

	suite.tfMock.destroyFunc = func(ctx context.Context, opts ...tfexec.DestroyOption) error {
		time.Sleep(20 * time.Millisecond)
		workDone = time.Now()
		return errors.New("test error")
	}

	suite.runWorker()

	assert.False(suite.T(), finalStatusSent.IsZero())
	assert.Less(suite.T(), finalStatusSent.Sub(workDone), 2*time.Second)
}

func (suite *WorkerTestSuite) Test_FinalStatusIsRetriedWhileMeshfedAnswers503() {
	suite.run = validRun(DESTROY.str(), "https://github.com/meshcloud/meshstack-hub.git", "modules/github/repository/buildingblock")

	finalStatusAttempts := 0
	suite.calls.update = func(req *http.Request) *http.Response {
		data, _ := io.ReadAll(req.Body)
		var update meshapi.MeshRunnerRunStatusUpdate
		json.Unmarshal(data, &update)
		status := http.StatusOK
		if *update.Status == FAILED.str() {
			finalStatusAttempts++
			if finalStatusAttempts <= 2 {
				status = http.StatusServiceUnavailable
			}
		}
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(bytes.NewBuffer([]byte("{}"))),
			Header:     make(http.Header),
		}
	}

	suite.tfMock.destroyFunc = func(ctx context.Context, opts ...tfexec.DestroyOption) error {
		return errors.New("test error")
	}

	suite.runWorker()

	assert.Equal(suite.T(), 3, finalStatusAttempts)
}

func (suite *WorkerTestSuite) Test_UpdatesStatusWithLiveLogs() {
	// let worker report status every half second
	suite.w.statusUpdateInterval = time.Millisecond * 500

	suite.tfMock.applyFunc = func(ctx context.Context, opts ...tfexec.ApplyOption) error {
		for i := range 5 {
			suite.tfMock.stdOut.Write([]byte(fmt.Sprintf("%d", i)))
			time.Sleep(time.Second * 1)
		}
		return nil
	}

	suite.run = validRun(APPLY.str(), "https://github.com/meshcloud/meshstack-hub.git", "modules/github/repository/buildingblock")

	updateCalls := make([]http.Request, 0)
	suite.calls.update = func(req *http.Request) *http.Response {
		updateCalls = append(updateCalls, *req)
		return noopCall(req)
	}

	suite.runWorker()
	assert.GreaterOrEqual(suite.T(), len(updateCalls), 9) // updates every 500ms, apply duration is minimum 5s

	// to verify, we check if there are at least the expected updates existing
	// with the systemMessages ["0", "01", "012", "0123", "01234"] in the execute_tf step,
	// prefixed with the "no predecessor plan" notice logged before a fresh apply.
	const noPredecessorNotice = "No plan artifact linked to this run; running a fresh terraform apply.\n"
	expectedLogUpdates := []string{"0", "01", "012", "0123", "01234"}
	for i, suffix := range expectedLogUpdates {
		expectedLogUpdates[i] = noPredecessorNotice + suffix
	}

	for _, expected := range expectedLogUpdates {
		found := false
		for _, update := range updateCalls {
			data, _ := io.ReadAll(update.Body)
			var update meshapi.MeshRunnerRunStatusUpdate
			json.Unmarshal(data, &update)
			if step := findStepOrNil(update, StepExecuteTf); step != nil && step.SystemMessage != nil && *step.SystemMessage == expected {
				found = true
				break
			}
		}
		assert.True(suite.T(), found, fmt.Sprintf("Expected update with systemMessage '%s' not found.", expected))
	}
}

func mockUpdateCallWithAbortResponse() func(_ *http.Request) *http.Response {
	return func(_ *http.Request) *http.Response {
		body, _ := json.Marshal(meshapi.MeshRunnerRunStatusUpdateResult{Abort: true})
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBuffer(body)),
			Header:     make(http.Header),
		}
	}
}

const scenarioRunUrl = "http://localhost/api/meshobjects/meshbuildingblockruns/run-uuid"

func validRun(behavior, repo, path string) meshapi.MeshRunnerRun {
	impl := meshapi.MeshRunnerTerraformImplementation{
		TerraformVersion: DEFAULT_TF_VER,
		RepositoryUrl:    repo,
		RepositoryPath:   new(path),
	}
	var links meshapi.MeshRunnerRunLinks
	if behavior == DETECT.str() {
		links.ArtifactUpload.Href = scenarioRunUrl + "/plan-artifact"
	}
	return scenarioRun(behavior, impl, links)
}

func applyRunWithPlanArtifact(repo, repoPath, planArtifactHref string) meshapi.MeshRunnerRun {
	impl := meshapi.MeshRunnerTerraformImplementation{TerraformVersion: DEFAULT_TF_VER, RepositoryUrl: repo, RepositoryPath: new(repoPath)}
	return scenarioRun(APPLY.str(), impl, meshapi.MeshRunnerRunLinks{PlanArtifact: meshapi.MeshRunnerRunLink{Href: planArtifactHref}})
}

func detectRunWithUploadLink(repo, repoPath, artifactUploadHref string) meshapi.MeshRunnerRun {
	impl := meshapi.MeshRunnerTerraformImplementation{TerraformVersion: DEFAULT_TF_VER, RepositoryUrl: repo, RepositoryPath: new(repoPath)}
	return scenarioRun(DETECT.str(), impl, meshapi.MeshRunnerRunLinks{ArtifactUpload: meshapi.MeshRunnerRunLink{Href: artifactUploadHref}})
}

func detectRunWithoutUploadLink(repo, repoPath string) meshapi.MeshRunnerRun {
	impl := meshapi.MeshRunnerTerraformImplementation{TerraformVersion: DEFAULT_TF_VER, RepositoryUrl: repo, RepositoryPath: new(repoPath)}
	return scenarioRun(DETECT.str(), impl, meshapi.MeshRunnerRunLinks{})
}

func scenarioRun(behavior string, impl meshapi.MeshRunnerTerraformImplementation, links meshapi.MeshRunnerRunLinks) meshapi.MeshRunnerRun {
	implJson, err := json.Marshal(impl)
	if err != nil {
		panic(err)
	}
	links.RegisterSource.Href = scenarioRunUrl + "/status/source"
	links.UpdateSource.Href = scenarioRunUrl + "/status/source/{sourceId}"
	return meshapi.MeshRunnerRun{
		ApiVersion: "v1",
		Kind:       "meshBuildingBlockRun",
		Metadata:   meshapi.MeshRunnerRunMetadata{Uuid: testRunUuid},
		Spec: meshapi.MeshRunnerRunSpec{
			RunNumber: 1,
			Behavior:  behavior,
			RunToken:  testRunToken,
			BuildingBlock: meshapi.MeshRunnerRunBuildingBlock{
				Uuid: "block-uuid",
				Spec: meshapi.MeshRunnerRunBuildingBlockSpec{DisplayName: "Test-BuildingBlock", Inputs: []meshapi.MeshRunnerRunInput{}},
			},
			Definition: meshapi.MeshRunnerRunDefinition{
				Uuid: "definition-uuid",
				Spec: meshapi.MeshRunnerRunDefinitionSpec{Version: 1, Implementation: implJson},
			},
		},
		Links: links,
	}
}

type testRoundTripper func(req *http.Request) *http.Response

func (t testRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return t(req), nil
}
