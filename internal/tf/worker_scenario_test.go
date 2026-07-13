package tf

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/stretchr/testify/suite"

	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

type WorkerTestSuite struct {
	suite.Suite
	w      *Worker
	calls  MockRunApiCalls
	tfBin  *TfBinaries
	tfMock *MockedTfFacade
	// meter records the D12 generic standalone-runner metrics (§4.3, step 4) -- checked by
	// meter_test.go's assertions layered onto the existing success/failure scenarios.
	meter *fakeMeter
	// repo is the hermetic local git fixture (CP1/F1) scenario tests clone from instead of
	// https://github.com/meshcloud/meshstack-hub.git; repoPath is the terraform-sources
	// subdirectory inside it, mirroring the real repo's "modules/github/repository/buildingblock".
	repo     *localGitRepo
	repoPath string
	// cfg is the runner config threaded into the Worker under test (FOLLOW_UP P2.3), replacing
	// the former package-level AppConfig global.
	cfg TfRunnerConfig
}

type MockRunApiCalls struct {
	fetch, register, update, download func(*http.Request) *http.Response
}

func noopCall(req *http.Request) *http.Response {
	return &http.Response{
		StatusCode: 200,
	}
}

func (suite *WorkerTestSuite) scenarioClientBehavior(req *http.Request) *http.Response {
	switch {
	// this is the predecessor plan-artifact download call
	case req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/plan-artifact"):
		return suite.calls.download(req)

	// this is the "status update" call
	case req.Method == http.MethodPatch && strings.Contains(req.URL.Path, "/status/source"):
		return suite.calls.update(req)

	// this is the "registration" call
	case req.Method == http.MethodPost && strings.Contains(req.URL.Path, "/status/source"):
		return suite.calls.register(req)

	// this is the "get run" call
	default:
		return suite.calls.fetch(req)
	}
}

func Test_WorkerSuite(t *testing.T) {
	suite.Run(t, new(WorkerTestSuite))
}

// run once before suite runs,
// setup worker and tfBinaries, use temp directories.
func (suite *WorkerTestSuite) SetupSuite() {

	testTfInstallDir, err := os.MkdirTemp(os.TempDir(), "workerScenario-tf-")
	if err != nil {
		panic(err)
	}

	tmpWd, err := os.MkdirTemp(os.TempDir(), "workerScenario-wd-")
	if err != nil {
		panic(err)
	}

	// runner config threaded into the Worker under test (FOLLOW_UP P2.3)
	suite.cfg = TfRunnerConfig{
		RunnerUuid:            "scenario-runner",
		SkipHostKeyValidation: false,
		InitTimeoutMins:       10,
		WsTimeoutMins:         10,
		TfCommandTimeoutMins:  10,
		TfParentWorkingDir:    tmpWd,
	}

	suite.tfMock = &MockedTfFacade{}
	suite.tfMock.initMockFuncs()
	suite.tfBin, err = ForTestNewTfBin(testTfInstallDir, io.Discard, suite.tfMock)
	if err != nil {
		panic(err)
	}

	suite.repoPath = "modules/github/repository/buildingblock"
	suite.repo = makeLocalGitRepo(suite.T(), map[string]string{
		suite.repoPath + "/main.tf": "# fixture terraform source, not executed (MockedTfFacade stubs tf calls)\n",
	})
}

// clean up temp directory after test suite ran.
func (suite *WorkerTestSuite) TearDownSuite() {
	_ = os.RemoveAll(suite.tfBin.dir)
	_ = os.RemoveAll(suite.cfg.TfParentWorkingDir)
}

// for each test setup new channels.
func (suite *WorkerTestSuite) SetupTest() {
	suite.calls = MockRunApiCalls{
		fetch:    noopCall,
		register: noopCall,
		update:   noopCall,
		download: noopCall,
	}

	suite.tfMock.initMockFuncs() // reset to default mock behavior

	scenarioAuth := &runApiAuth{baseAuth: meshapi.BasicAuth{Username: "test-user", Password: "test-pass"}}

	// Wire the injected decryptor (D4) with the checked-in test key pair so the decrypt path runs
	// end to end for sensitive-input scenarios (cp4), replacing the former meshcrypto.Crypto global.
	dec := testDecryptor(suite.T())

	suite.meter = &fakeMeter{}

	suite.w = &Worker{
		workerNumber:         1,
		tfBinaries:           suite.tfBin,
		workerIn:             make(chan workerToken, 2),
		workerOut:            make(chan workerToken, 2),
		runApi:               newScenarioRunApiClient("scenario-runner", scenarioAuth, testRoundTripper(suite.scenarioClientBehavior), dec),
		log:                  slog.New(slog.NewTextHandler(io.Discard, nil)),
		timeout:              30 * time.Second,
		statusUpdateInterval: time.Second * 10,
		dec:                  dec,
		meter:                suite.meter,
		cfg:                  suite.cfg.exec(),
	}
}

// clean up after test.
func (suite *WorkerTestSuite) TearDownTest() {
	close(suite.w.workerIn)
	close(suite.w.workerOut)
}

func (suite *WorkerTestSuite) runWorker() {
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		suite.w.work()
		wg.Done()
	}()

	// simulate manager interaction
	suite.w.workerIn <- work
	suite.w.workerIn <- stop

	wg.Wait()
}

// findStep returns the step with the given ID from a status update.
// It immediately fails the test if no matching step is found.
func findStep(tb testing.TB, update meshapi.RunStatusUpdateDTO, stepId string) meshapi.StepStatusUpdateDTO {
	tb.Helper()
	if s := findStepOrNil(update, stepId); s != nil {
		return *s
	}
	ids := make([]string, len(update.Steps))
	for i, s := range update.Steps {
		ids[i] = s.Id
	}
	tb.Fatalf("step %q not found; available step IDs: %v", stepId, ids)
	return meshapi.StepStatusUpdateDTO{}
}

// findStepOrNil returns the step with the given ID, or nil if not present.
// Prefer findStep in tests that require the step to exist.
func findStepOrNil(update meshapi.RunStatusUpdateDTO, stepId string) *meshapi.StepStatusUpdateDTO {
	for i := range update.Steps {
		if update.Steps[i].Id == stepId {
			return &update.Steps[i]
		}
	}
	return nil
}

func (suite *WorkerTestSuite) Test_MissingAuth() {
	// a filesystem path that is guaranteed not to be a git repository — go-git's clone fails the
	// same way it would for an unreachable/nonexistent remote ("repository not found"), hermetically.
	doesNotExist := filepath.Join(suite.T().TempDir(), "does-not-exist")
	suite.calls.fetch = mockValidRunDetailsFetchCall(APPLY.str(), doesNotExist, "")

	updateCalls := make([]http.Request, 0)
	suite.calls.update = func(req *http.Request) *http.Response {
		updateCalls = append(updateCalls, *req)
		return nil
	}

	// execute worker
	suite.runWorker()

	// assertions
	suite.Len(updateCalls, 1)

	data, _ := io.ReadAll(updateCalls[0].Body)
	var update meshapi.RunStatusUpdateDTO
	suite.Require().NoError(json.Unmarshal(data, &update))

	suite.Equal(FAILED.str(), *update.Status)
	suite.Len(update.Steps, 6)
	for i, step := range update.Steps {
		suite.Equal(FAILED.str(), *step.Status)
		if i == 0 {
			suite.Contains(*step.SystemMessage, "copy sources from")
			// UserMessage is now populated with the actual error to improve panel visibility
			suite.NotNil(step.UserMessage)
		} else {
			suite.Equal("Aborted due to failure in an earlier step", *step.SystemMessage)
			suite.Nil(step.UserMessage)
		}
	}
}

func (suite *WorkerTestSuite) Test_ApplySucceeded() {
	suite.calls.fetch = mockValidRunDetailsFetchCall(APPLY.str(), suite.repo.Path, suite.repoPath)

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
	suite.GreaterOrEqual(len(updateCalls), 1)

	// Check the final update call
	lastUpdate := updateCalls[len(updateCalls)-1]
	data, _ := io.ReadAll(lastUpdate.Body)
	var update meshapi.RunStatusUpdateDTO
	suite.Require().NoError(json.Unmarshal(data, &update))

	suite.Equal(SUCCEEDED.str(), *update.Status)
	for _, step := range update.Steps {
		suite.Equal(SUCCEEDED.str(), *step.Status)
		suite.Nil(step.UserMessage)
	}
	suite.Nil(update.Summary)

	// D12 (§4.3, step 4): a successful run is claimed once and counted as a success, never
	// as a failure.
	counts := suite.meter.snapshot()
	suite.Equal(meterCounts{claimed: 1, succeeded: 1, failed: 0, pollErrors: 0}, counts)
}

func (suite *WorkerTestSuite) Test_RegistrationConflict_ContinuesExecution() {
	// Regression test for Kubernetes pod retries:
	// When a pod is restarted (e.g. due to node pressure), the replacement pod's Register()
	// call receives 409 Conflict because the source was already registered by the previous pod.
	// The runner must treat 409 as idempotent and continue executing — it must NEVER report
	// PENDING status to the API (which would cause a 500 from the coordinator's state machine).

	suite.calls.fetch = mockValidRunDetailsFetchCall(APPLY.str(), suite.repo.Path, suite.repoPath)

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

		var update meshapi.RunStatusUpdateDTO
		suite.Require().NoError(json.Unmarshal(data, &update))

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
	suite.GreaterOrEqual(len(capturedUpdates), 1, "expected at least one status update")

	// PENDING must NEVER appear in any update — it is a coordinator-only status and
	// sending it would cause the coordinator's state machine to reject it with a 500
	for _, u := range capturedUpdates {
		suite.NotEqual(PENDING.str(), u.status,
			"runner must never report PENDING status to the API (coordinator rejects it)")
	}

	// Final update must indicate successful execution
	finalStatus := capturedUpdates[len(capturedUpdates)-1].status
	suite.Equal(SUCCEEDED.str(), finalStatus,
		"run should complete successfully even when registration returns 409")
}

func (suite *WorkerTestSuite) Test_ApplyRunAborted() {
	// simulate an init tf call that needs 11s to finish
	suite.tfMock.initFunc = func(ctx context.Context, opts ...tfexec.InitOption) error {
		time.Sleep(time.Second * 11)
		return nil
	}

	// we test that apply is called with a cancelled context because run is aborted before
	suite.tfMock.applyFunc = func(ctx context.Context, opts ...tfexec.ApplyOption) error {
		suite.Equal(context.Canceled, ctx.Err())
		return nil
	}

	suite.calls.fetch = mockValidRunDetailsFetchCall(APPLY.str(), suite.repo.Path, suite.repoPath)

	updateCalls := make([]http.Request, 0)
	suite.calls.update = func(req *http.Request) *http.Response {
		updateCalls = append(updateCalls, *req)
		return mockUpdateCallWithAbortResponse()(req)
	}

	// execute worker
	suite.runWorker()

	// assertions

	// we expect that init is called, but as the first update returns a positive 'abort' flag, apply will be called
	// with a context that has been cancelled already.
	// therefore also not more then 1 update call is sent (11s duration / 10sec update interval)
	// update will have the IN_PROGRESS state, as we are not done yet
	suite.Len(updateCalls, 1)
	data, _ := io.ReadAll(updateCalls[0].Body)
	var update meshapi.RunStatusUpdateDTO
	suite.Require().NoError(json.Unmarshal(data, &update))
	suite.Equal(IN_PROGRESS.str(), *update.Status)
}

func (suite *WorkerTestSuite) Test_ApplyTfFailure() {
	suite.calls.fetch = mockValidRunDetailsFetchCall(APPLY.str(), suite.repo.Path, suite.repoPath)

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
		_, _ = suite.tfMock.stdOut.Write([]byte("apply in progress\n"))
		_, _ = suite.tfMock.stdErr.Write([]byte("failure\n"))
		return errors.New("test error")
	}

	// execute worker
	suite.runWorker()

	// assertions - now we expect multiple update calls due to error logging
	suite.GreaterOrEqual(len(updateCalls), 1)

	// Check the final update call
	lastUpdate := updateCalls[len(updateCalls)-1]
	data, _ := io.ReadAll(lastUpdate.Body)
	var update meshapi.RunStatusUpdateDTO
	suite.Require().NoError(json.Unmarshal(data, &update))

	suite.Equal(FAILED.str(), *update.Status)
	suite.Len(update.Steps, 6)

	sources := findStep(suite.T(), update, StepSources)
	suite.Equal(SUCCEEDED.str(), *sources.Status)
	suite.Nil(sources.UserMessage)

	inputStep := findStep(suite.T(), update, StepInput)
	suite.Equal(SUCCEEDED.str(), *inputStep.Status)
	suite.Nil(inputStep.UserMessage)

	executeTf := findStep(suite.T(), update, StepExecuteTf)
	suite.Equal(FAILED.str(), *executeTf.Status)
	// UserMessage is now set to the error text for better panel visibility
	suite.NotNil(executeTf.UserMessage)
	suite.Equal("test error", *executeTf.UserMessage)
	suite.Equal("No plan artifact linked to this run; running a fresh terraform apply.\napply in progress\nfailure\ntest error\n", *executeTf.SystemMessage) // includes error message

	outputStep := findStep(suite.T(), update, StepOutput)
	suite.Equal(FAILED.str(), *outputStep.Status)
	suite.Nil(outputStep.UserMessage)
	suite.Equal("Aborted due to failure in an earlier step", *outputStep.SystemMessage)

	// D12 (§4.3, step 4): a run that reaches a FAILED terminal status is claimed once and
	// counted as a failure, never as a success.
	counts := suite.meter.snapshot()
	suite.Equal(meterCounts{claimed: 1, succeeded: 0, failed: 1, pollErrors: 0}, counts)
}

func (suite *WorkerTestSuite) Test_DestroySucceeded() {
	suite.calls.fetch = mockValidRunDetailsFetchCall(DESTROY.str(), suite.repo.Path, suite.repoPath)

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
	suite.GreaterOrEqual(len(updateCalls), 1)

	// Check the final update call
	lastUpdate := updateCalls[len(updateCalls)-1]
	data, _ := io.ReadAll(lastUpdate.Body)
	var update meshapi.RunStatusUpdateDTO
	suite.Require().NoError(json.Unmarshal(data, &update))

	suite.Equal(SUCCEEDED.str(), *update.Status)
	for _, step := range update.Steps {
		suite.Equal(SUCCEEDED.str(), *step.Status)
		suite.Nil(step.UserMessage)
	}
	suite.Nil(update.Summary)
}

func (suite *WorkerTestSuite) Test_DestroyTfFailure() {
	suite.calls.fetch = mockValidRunDetailsFetchCall(DESTROY.str(), suite.repo.Path, suite.repoPath)

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
		_, _ = suite.tfMock.stdOut.Write([]byte("destroy in progress\n"))
		_, _ = suite.tfMock.stdErr.Write([]byte("failure\n"))
		return errors.New("test error")
	}

	// execute worker
	suite.runWorker()

	// assertions - now we expect multiple update calls due to error logging
	suite.GreaterOrEqual(len(updateCalls), 1)

	// Check the final update call
	lastUpdate := updateCalls[len(updateCalls)-1]
	data, _ := io.ReadAll(lastUpdate.Body)
	var update meshapi.RunStatusUpdateDTO
	suite.Require().NoError(json.Unmarshal(data, &update))

	suite.Equal(FAILED.str(), *update.Status)
	suite.Len(update.Steps, 6)

	sources := findStep(suite.T(), update, StepSources)
	suite.Equal(SUCCEEDED.str(), *sources.Status)
	suite.Nil(sources.UserMessage)

	inputStep := findStep(suite.T(), update, StepInput)
	suite.Equal(SUCCEEDED.str(), *inputStep.Status)
	suite.Nil(inputStep.UserMessage)

	executeTf := findStep(suite.T(), update, StepExecuteTf)
	suite.Equal(FAILED.str(), *executeTf.Status)
	// UserMessage is now set to the error text for better panel visibility
	suite.NotNil(executeTf.UserMessage)
	suite.Equal("test error", *executeTf.UserMessage)
	suite.Equal("destroy in progress\nfailure\ntest error\n", *executeTf.SystemMessage) // includes error message

	outputStep := findStep(suite.T(), update, StepOutput)
	suite.Equal(FAILED.str(), *outputStep.Status)
	suite.Nil(outputStep.UserMessage)
	suite.Equal("Aborted due to failure in an earlier step", *outputStep.SystemMessage)
}

func (suite *WorkerTestSuite) Test_UpdatesStatusWithLiveLogs() {
	// let worker report status every half second
	suite.w.statusUpdateInterval = time.Millisecond * 500

	suite.tfMock.applyFunc = func(ctx context.Context, opts ...tfexec.ApplyOption) error {
		for i := 0; i < 5; i++ {
			_, _ = fmt.Fprintf(suite.tfMock.stdOut, "%d", i)
			time.Sleep(time.Second * 1)
		}
		return nil
	}

	suite.calls.fetch = mockValidRunDetailsFetchCall(APPLY.str(), suite.repo.Path, suite.repoPath)

	updateCalls := make([]http.Request, 0)
	suite.calls.update = func(req *http.Request) *http.Response {
		updateCalls = append(updateCalls, *req)
		return nil
	}

	suite.runWorker()
	suite.GreaterOrEqual(len(updateCalls), 9) // updates every 500ms, apply duration is minimum 5s

	// to verify, we check if there are at least the expected updates existing
	// with the systemMessages ["0", "01", "012", "0123", "01234"] in the execute_tf step,
	// prefixed with the "no predecessor plan" notice logged before a fresh apply.
	const noPredecessorNotice = "No plan artifact linked to this run; running a fresh terraform apply.\n"
	expectedLogUpdates := []string{"0", "01", "012", "0123", "01234"}
	for i, suffix := range expectedLogUpdates {
		expectedLogUpdates[i] = noPredecessorNotice + suffix
	}

	// Decode every captured request body exactly once upfront: req.Body is a single-read stream,
	// and the expectedLogUpdates loop below scans the full set of decoded updates once per
	// expected value, so re-reading req.Body per scan would silently see an already-drained
	// (empty) body on the second and later scans.
	decodedUpdates := make([]meshapi.RunStatusUpdateDTO, len(updateCalls))
	for i, update := range updateCalls {
		data, err := io.ReadAll(update.Body)
		suite.Require().NoError(err)
		suite.Require().NoError(json.Unmarshal(data, &decodedUpdates[i]))
	}

	for _, expected := range expectedLogUpdates {
		found := false
		for _, update := range decodedUpdates {
			if step := findStepOrNil(update, StepExecuteTf); step != nil && step.SystemMessage != nil && *step.SystemMessage == expected {
				found = true
				break
			}
		}
		suite.True(found, "Expected update with systemMessage '%s' not found.", expected)
	}
}

func mockUpdateCallWithAbortResponse() func(_ *http.Request) *http.Response {
	return func(_ *http.Request) *http.Response {
		body, _ := json.Marshal(
			&meshapi.RunUpdateResponseDTO{
				Abort: true,
			},
		)
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBuffer(body)),
			Header:     make(http.Header),
		}
	}
}

// mockValidRunDetailsFetchCall and mockApplyRunWithPlanArtifactFetchCall are the pre-CP1 fixture
// helpers, kept as thin named wrappers over the shared runDetailsFetchCall builder (fixtures_test.go)
// so every existing call site ports over with no other change.
func mockValidRunDetailsFetchCall(behavior, repo, path string) func(_ *http.Request) *http.Response {
	return runDetailsFetchCall(withBehavior(behavior), withRepo(repo, path))
}

func mockApplyRunWithPlanArtifactFetchCall(repo, repoPath, planArtifactHref string) func(_ *http.Request) *http.Response {
	return runDetailsFetchCall(withRepo(repo, repoPath), withPlanArtifact(planArtifactHref))
}

// returns pointer of given value to be able to inline value without var usage.
func p[T any](v T) *T {
	return &v
}

type testRoundTripper func(req *http.Request) *http.Response

func (t testRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return t(req), nil
}
