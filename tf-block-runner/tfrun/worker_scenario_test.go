package tfrun

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
)

type WorkerTestSuite struct {
	suite.Suite
	w      *Worker
	calls  MockRunApiCalls
	tfBin  *TfBinaries
	tfMock *MockedTfFacade
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
// setup worker and tfBinaries, use temp directories
func (suite *WorkerTestSuite) SetupSuite() {

	testTfInstallDir, err := os.MkdirTemp(os.TempDir(), "workerScenario-tf-")
	if err != nil {
		panic(err)
	}

	tmpWd, err := os.MkdirTemp(os.TempDir(), "workerScenario-wd-")
	if err != nil {
		panic(err)
	}

	// setup statically referenced app config
	AppConfig = TfRunnerConfig{
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
}

// clean up temp directory after test suite ran
func (suite *WorkerTestSuite) TearDownSuite() {
	os.RemoveAll(suite.tfBin.dir)
	os.RemoveAll(AppConfig.TfParentWorkingDir)
}

// for each test setup new channels
func (suite *WorkerTestSuite) SetupTest() {
	suite.calls = MockRunApiCalls{
		fetch:    noopCall,
		register: noopCall,
		update:   noopCall,
		download: noopCall,
	}

	suite.tfMock.initMockFuncs() // reset to default mock behavior

	// Create basic auth for test API client
	basicAuth := base64.StdEncoding.EncodeToString([]byte("test-user:test-pass"))
	_ = basicAuth // retained for clarity; credentials encoded in BasicAuth below
	scenarioAuth := &runApiAuth{baseAuth: meshapi.BasicAuth{Username: "test-user", Password: "test-pass"}}
	mockHC := &http.Client{Transport: testRoundTripper(suite.scenarioClientBehavior)}

	suite.w = &Worker{
		workerNumber: 1,
		tfBinaries:   suite.tfBin,
		workerIn:     make(chan workerToken, 2),
		workerOut:    make(chan workerToken, 2),
		runApi: &RunApiClient{
			rid:        "scenario-runner",
			baseURL:    "",
			auth:       scenarioAuth,
			client:     meshapi.NewClientWithHTTP("", "scenario-runner", scenarioAuth, mockHC),
			httpClient: mockHC,
		},
		log:                  log.New(io.Discard, "", log.LstdFlags),
		timeout:              30 * time.Second,
		statusUpdateInterval: time.Second * 10,
	}
}

// clean up after test
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
func findStep(t testing.TB, update meshapi.RunStatusUpdateDTO, stepId string) meshapi.StepStatusUpdateDTO {
	t.Helper()
	if s := findStepOrNil(update, stepId); s != nil {
		return *s
	}
	ids := make([]string, len(update.Steps))
	for i, s := range update.Steps {
		ids[i] = s.Id
	}
	t.Fatalf("step %q not found; available step IDs: %v", stepId, ids)
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
	suite.calls.fetch = mockValidRunDetailsFetchCall(APPLY.str(), "https://github.com/meshcloud/does-not-exist.git", "")

	updateCalls := make([]http.Request, 0)
	suite.calls.update = func(req *http.Request) *http.Response {
		updateCalls = append(updateCalls, *req)
		return nil
	}

	// execute worker
	suite.runWorker()

	// assertions
	assert.Equal(suite.T(), 1, len(updateCalls))

	data, _ := io.ReadAll(updateCalls[0].Body)
	var update meshapi.RunStatusUpdateDTO
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
	suite.calls.fetch = mockValidRunDetailsFetchCall(APPLY.str(), "https://github.com/meshcloud/meshstack-hub.git", "modules/github/repository/buildingblock")

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
	var update meshapi.RunStatusUpdateDTO
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

	suite.calls.fetch = mockValidRunDetailsFetchCall(APPLY.str(), "https://github.com/meshcloud/meshstack-hub.git", "modules/github/repository/buildingblock")

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

func (suite *WorkerTestSuite) Test_ApplyRunAborted() {
	// simulate an init tf call that needs 11s to finish
	suite.tfMock.initFunc = func(ctx context.Context, opts ...tfexec.InitOption) error {
		time.Sleep(time.Second * 11)
		return nil
	}

	// we test that apply is called with a cancelled context because run is aborted before
	suite.tfMock.applyFunc = func(ctx context.Context, opts ...tfexec.ApplyOption) error {
		assert.Equal(suite.T(), context.Canceled, ctx.Err())
		return nil
	}

	suite.calls.fetch = mockValidRunDetailsFetchCall(APPLY.str(), "https://github.com/meshcloud/meshstack-hub.git", "modules/github/repository/buildingblock")

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
	assert.Equal(suite.T(), 1, len(updateCalls))
	data, _ := io.ReadAll(updateCalls[0].Body)
	var update meshapi.RunStatusUpdateDTO
	json.Unmarshal(data, &update)
	assert.Equal(suite.T(), IN_PROGRESS.str(), *update.Status)
}

func (suite *WorkerTestSuite) Test_ApplyTfFailure() {
	suite.calls.fetch = mockValidRunDetailsFetchCall(APPLY.str(), "https://github.com/meshcloud/meshstack-hub.git", "modules/github/repository/buildingblock")

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
	var update meshapi.RunStatusUpdateDTO
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
	assert.Equal(suite.T(), "apply in progress\nfailure\ntest error\n", *executeTf.SystemMessage) // includes error message

	outputStep := findStep(suite.T(), update, StepOutput)
	assert.Equal(suite.T(), FAILED.str(), *outputStep.Status)
	assert.Nil(suite.T(), outputStep.UserMessage)
	assert.Equal(suite.T(), "Aborted due to failure in an earlier step", *outputStep.SystemMessage)
}

func (suite *WorkerTestSuite) Test_DestroySucceeded() {
	suite.calls.fetch = mockValidRunDetailsFetchCall(DESTROY.str(), "https://github.com/meshcloud/meshstack-hub.git", "modules/github/repository/buildingblock")

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
	var update meshapi.RunStatusUpdateDTO
	json.Unmarshal(data, &update)

	assert.Equal(suite.T(), SUCCEEDED.str(), *update.Status)
	for _, step := range update.Steps {
		assert.Equal(suite.T(), SUCCEEDED.str(), *step.Status)
		assert.Nil(suite.T(), step.UserMessage)
	}
	assert.Nil(suite.T(), update.Summary)
}

func (suite *WorkerTestSuite) Test_DestroyTfFailure() {
	suite.calls.fetch = mockValidRunDetailsFetchCall(DESTROY.str(), "https://github.com/meshcloud/meshstack-hub.git", "modules/github/repository/buildingblock")

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
	var update meshapi.RunStatusUpdateDTO
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

func (suite *WorkerTestSuite) Test_UpdatesStatusWithLiveLogs() {
	// let worker report status every half second
	suite.w.statusUpdateInterval = time.Millisecond * 500

	suite.tfMock.applyFunc = func(ctx context.Context, opts ...tfexec.ApplyOption) error {
		for i := 0; i < 5; i++ {
			suite.tfMock.stdOut.Write([]byte(fmt.Sprintf("%d", i)))
			time.Sleep(time.Second * 1)
		}
		return nil
	}

	suite.calls.fetch = mockValidRunDetailsFetchCall(APPLY.str(), "https://github.com/meshcloud/meshstack-hub.git", "modules/github/repository/buildingblock")

	updateCalls := make([]http.Request, 0)
	suite.calls.update = func(req *http.Request) *http.Response {
		updateCalls = append(updateCalls, *req)
		return nil
	}

	suite.runWorker()
	assert.GreaterOrEqual(suite.T(), len(updateCalls), 9) // updates every 500ms, apply duration is minimum 5s

	// to verify, we check if there are at least the expected updates existing
	// with the systemMessages ["0", "01", "012", "0123", "01234"] in the execute_tf step
	expectedLogUpdates := []string{"0", "01", "012", "0123", "01234"}

	for _, expected := range expectedLogUpdates {
		found := false
		for _, update := range updateCalls {
			data, _ := io.ReadAll(update.Body)
			var update meshapi.RunStatusUpdateDTO
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

func mockValidRunDetailsFetchCall(behavior, repo, path string) func(_ *http.Request) *http.Response {
	return func(_ *http.Request) *http.Response {
		implDTO := meshapi.TerraformImplementation{
			TerraformVersion: DEFAULT_TF_VER,
			RepositoryUrl:    repo,
			RepositoryPath:   p(path),
			RefName:          nil,
			SshPrivateKey:    nil,
			KnownHost:        nil,
			Async:            false,
		}
		implJSON, _ := json.Marshal(implDTO)
		body, _ := json.Marshal(
			&meshapi.RunDetailsDTO{
				ApiVersion: "v1",
				Kind:       "MeshBuildingBlockRun",
				Metadata: meshapi.RunMetaDTO{
					Uuid: "run-uuid",
				},
				Spec: meshapi.RunSpecDTO{
					RunNumber: 1,
					Behavior:  behavior,
					RunToken:  "test-mock-run-token-12345",
					BuildingBlock: meshapi.BuildingBlockSpecDTO{
						Uuid: "block-uuid",
						Spec: meshapi.BuildingBlockDetailsSpecDTO{
							DisplayName: "Test-BuildingBlock",
							Inputs:      make([]meshapi.BuildingBlockInputSpecDTO, 0),
						},
					},
					Definition: meshapi.DefinitionSpecDTO{
						Uuid: "definition-uuid",
						Spec: meshapi.DefinitionDetailsSpecDTO{
							Version:        1,
							Implementation: implJSON,
						},
					},
				},
			},
		)
		header := make(http.Header)
		header.Add("Content-Type", meshapi.BlockRunMediaTypeV1)

		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBuffer(body)),
			Header:     header,
		}
	}
}

// mockApplyRunWithPlanArtifactFetchCall builds an APPLY run-details response whose _links contain
// a planArtifact href, signalling the runner to apply a predecessor's saved plan instead of
// re-planning.
func mockApplyRunWithPlanArtifactFetchCall(repo, repoPath, planArtifactHref string) func(_ *http.Request) *http.Response {
	return func(_ *http.Request) *http.Response {
		implDTO := meshapi.TerraformImplementation{
			TerraformVersion: DEFAULT_TF_VER,
			RepositoryUrl:    repo,
			RepositoryPath:   p(repoPath),
			Async:            false,
		}
		implJSON, _ := json.Marshal(implDTO)
		body, _ := json.Marshal(
			&meshapi.RunDetailsDTO{
				ApiVersion: "v1",
				Kind:       "MeshBuildingBlockRun",
				Metadata:   meshapi.RunMetaDTO{Uuid: "run-uuid"},
				Spec: meshapi.RunSpecDTO{
					RunNumber: 1,
					Behavior:  APPLY.str(),
					RunToken:  "test-mock-run-token-12345",
					BuildingBlock: meshapi.BuildingBlockSpecDTO{
						Uuid: "block-uuid",
						Spec: meshapi.BuildingBlockDetailsSpecDTO{
							DisplayName: "Test-BuildingBlock",
							Inputs:      make([]meshapi.BuildingBlockInputSpecDTO, 0),
						},
					},
					Definition: meshapi.DefinitionSpecDTO{
						Uuid: "definition-uuid",
						Spec: meshapi.DefinitionDetailsSpecDTO{
							Version:        1,
							Implementation: implJSON,
						},
					},
				},
				Links: meshapi.LinksDTO{
					PlanArtifact: meshapi.LinkDTO{Href: planArtifactHref},
				},
			},
		)
		header := make(http.Header)
		header.Add("Content-Type", meshapi.BlockRunMediaTypeV1)

		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBuffer(body)),
			Header:     header,
		}
	}
}

// returns pointer of given value to be able to inline value without var usage
func p[T any](v T) *T {
	return &v
}

type testRoundTripper func(req *http.Request) *http.Response

func (t testRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return t(req), nil
}
