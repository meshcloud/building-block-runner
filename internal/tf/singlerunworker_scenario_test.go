package tf

// CP3 (PLAN_DETAIL_01_tf_characterization_tests.md §9): SingleRunWorker scenario suite, mirroring
// WorkerTestSuite (worker_scenario_test.go) for the k8s single-run execution path
// (EXECUTION_MODE=single-run, D9 pin 16). Unlike the polling Worker, SingleRunWorker.ExecuteRun
// takes an already-fetched, already-decrypted *Run directly — there is no FetchRunDetails call in
// this path (the controller/main.go glue reads RUN_JSON_FILE_PATH and calls
// ToInternalWithoutDecryption before handing the Run to ExecuteRun) — so these scenarios build the
// Run via that same conversion (pin 16a) and drive ExecuteRun in-process instead of the
// work/stop channel protocol.
//
// Shares fixtures_test.go (CP1: local git repo, runDetailsDTO builder) and MockRunApiCalls/
// testRoundTripper/newScenarioRunApiClient/mockValidRunDetailsFetchCall helpers from
// worker_scenario_test.go; the fake-transport request router below is this suite's own copy of
// WorkerTestSuite.scenarioClientBehavior (kept file-local/disjoint so both suites can be authored
// and landed independently).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/stretchr/testify/suite"

	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

type SingleRunWorkerTestSuite struct {
	suite.Suite
	calls  MockRunApiCalls
	tfBin  *TfBinaries
	tfMock *MockedTfFacade
	// repo/repoPath: same hermetic local-git fixture pattern as WorkerTestSuite (CP1/F1).
	repo     *localGitRepo
	repoPath string
	// workerDir is a real, pre-existing directory: unlike Worker.tfExecution, SingleRunWorker.
	// ExecuteRun MkdirAll's workerDir itself before use (singlerunworker.go:54) — a k8s single-run
	// Job's working-dir mount may not exist yet — so "" (which Worker's tests rely on falling back
	// to os.TempDir() via os.MkdirTemp) would fail here (os.MkdirAll("", ...) errors).
	workerDir string
}

func Test_SingleRunWorkerSuite(t *testing.T) {
	suite.Run(t, new(SingleRunWorkerTestSuite))
}

func (suite *SingleRunWorkerTestSuite) SetupSuite() {
	testTfInstallDir, err := os.MkdirTemp(os.TempDir(), "singleRunScenario-tf-")
	if err != nil {
		panic(err)
	}

	tmpWd, err := os.MkdirTemp(os.TempDir(), "singleRunScenario-wd-")
	if err != nil {
		panic(err)
	}
	suite.workerDir = tmpWd

	// Shared package-level config (WorkerTestSuite sets the same global in its own SetupSuite);
	// safe because testify suites registered via top-level Test_* functions run sequentially in
	// one test binary, never concurrently.
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

	suite.repoPath = "modules/github/repository/buildingblock"
	suite.repo = makeLocalGitRepo(suite.T(), map[string]string{
		suite.repoPath + "/main.tf": "# fixture terraform source, not executed (MockedTfFacade stubs tf calls)\n",
	})
}

func (suite *SingleRunWorkerTestSuite) TearDownSuite() {
	_ = os.RemoveAll(suite.tfBin.dir)
	_ = os.RemoveAll(suite.workerDir)
}

func (suite *SingleRunWorkerTestSuite) SetupTest() {
	suite.calls = MockRunApiCalls{
		fetch:    noopCall,
		register: noopCall,
		update:   noopCall,
		download: noopCall,
	}
	suite.tfMock.initMockFuncs() // reset to default mock behavior
}

// scenarioClientBehavior routes the fake transport's requests the same way
// WorkerTestSuite.scenarioClientBehavior does; kept as this suite's own method (disjoint file) so
// it reads suite.calls off *this* suite type.
func (suite *SingleRunWorkerTestSuite) scenarioClientBehavior(req *http.Request) *http.Response {
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

	// SingleRunWorker never calls FetchRunDetails (no polling "get run" request in this path);
	// any other request is a test-authoring mistake.
	default:
		panic("unexpected request in SingleRunWorker scenario: " + req.Method + " " + req.URL.Path)
	}
}

// newWorker builds a SingleRunWorker wired to the fake transport with runToken-only auth (no
// baseAuth) — the k8s single-run contract (D9 pin 16b): the API client used by SingleRunWorker
// must authenticate every request with "Bearer <runToken>", never falling back to Basic auth,
// because single-run mode has no configured RunApiConfig credentials (config.go's
// validateAuthConfig explicitly waives them in this mode).
func (suite *SingleRunWorkerTestSuite) newWorker(runToken string) *SingleRunWorker {
	auth := &runApiAuth{runToken: p(runToken)}
	api := newScenarioRunApiClient("scenario-runner", auth, testRoundTripper(suite.scenarioClientBehavior), NoopDecryptor{})

	return &SingleRunWorker{
		workerDir:            suite.workerDir,
		timeout:              30 * time.Second,
		runApi:               api,
		tfBinaries:           suite.tfBin,
		log:                  log.New(io.Discard, "", log.LstdFlags),
		statusUpdateInterval: time.Second * 10,
	}
}

// buildRun builds a *Run the way the k8s single-run entrypoint does: main.go reads the run JSON
// from RUN_JSON_FILE_PATH after the controller has already decrypted sensitive inputs, and converts
// it via ToInternalWithoutDecryption — never runDTOToInternal, which decrypts again (D9 pin 16a).
func (suite *SingleRunWorkerTestSuite) buildRun(opts ...runDetailsOption) *Run {
	dto := runDetailsDTO(opts...)
	run, err := ToInternalWithoutDecryption(dto, NoopDecryptor{})
	suite.Require().NoError(err)
	return run
}

// collectUpdates installs a suite.calls.update handler that records every status-update request and
// acks with 200 + "{}" (no abort, no error) — the same shape the pre-existing WorkerTestSuite tests
// use for their "happy path" assertions.
func (suite *SingleRunWorkerTestSuite) collectUpdates() *[]http.Request {
	updateCalls := make([]http.Request, 0)
	suite.calls.update = func(req *http.Request) *http.Response {
		updateCalls = append(updateCalls, *req)
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBuffer([]byte("{}"))),
			Header:     make(http.Header),
		}
	}
	return &updateCalls
}

func decodeUpdate(tb testing.TB, req http.Request) meshapi.RunStatusUpdateDTO {
	tb.Helper()
	data, err := io.ReadAll(req.Body)
	if err != nil {
		tb.Fatalf("reading update body: %v", err)
	}
	var update meshapi.RunStatusUpdateDTO
	if err := json.Unmarshal(data, &update); err != nil {
		tb.Fatalf("unmarshalling update body: %v", err)
	}
	return update
}

// --- success paths (mirrors WorkerTestSuite's Test_ApplySucceeded/Test_DestroySucceeded/
// Test_DetectSucceeded_ArtifactInStatusUpdate) --------------------------------------------------

func (suite *SingleRunWorkerTestSuite) Test_ExecuteRun_ApplySucceeded() {
	w := suite.newWorker("single-run-token-apply-ok")
	run := suite.buildRun(withBehavior(APPLY.str()), withRepo(suite.repo.Path, suite.repoPath))

	updateCalls := suite.collectUpdates()

	suite.Require().NoError(w.ExecuteRun(run))

	suite.Require().GreaterOrEqual(len(*updateCalls), 1)
	last := decodeUpdate(suite.T(), (*updateCalls)[len(*updateCalls)-1])
	suite.Equal(SUCCEEDED.str(), *last.Status)
	for _, step := range last.Steps {
		suite.Equal(SUCCEEDED.str(), *step.Status)
		suite.Nil(step.UserMessage)
	}
	suite.Nil(last.Summary)
}

func (suite *SingleRunWorkerTestSuite) Test_ExecuteRun_DestroySucceeded() {
	w := suite.newWorker("single-run-token-destroy-ok")
	run := suite.buildRun(withBehavior(DESTROY.str()), withRepo(suite.repo.Path, suite.repoPath))

	updateCalls := suite.collectUpdates()

	suite.Require().NoError(w.ExecuteRun(run))

	suite.Require().GreaterOrEqual(len(*updateCalls), 1)
	last := decodeUpdate(suite.T(), (*updateCalls)[len(*updateCalls)-1])
	suite.Equal(SUCCEEDED.str(), *last.Status)
	for _, step := range last.Steps {
		suite.Equal(SUCCEEDED.str(), *step.Status)
	}
}

func (suite *SingleRunWorkerTestSuite) Test_ExecuteRun_DetectSucceeded_ArtifactInStatusUpdate() {
	planBytes := []byte("fake-plan-binary-data-single-run")
	planFuncCalled := false
	suite.tfMock.planFunc = func(ctx context.Context, opts ...tfexec.PlanOption) (bool, error) {
		planFuncCalled = true
		return true, os.WriteFile(suite.tfMock.artifactPath(), planBytes, 0600)
	}

	w := suite.newWorker("single-run-token-detect-ok")
	run := suite.buildRun(withBehavior(DETECT.str()), withRepo(suite.repo.Path, suite.repoPath))

	updateCalls := suite.collectUpdates()

	suite.Require().NoError(w.ExecuteRun(run))

	suite.True(planFuncCalled, "expected DETECT behavior to invoke terraform plan")
	suite.Require().GreaterOrEqual(len(*updateCalls), 1)
	last := decodeUpdate(suite.T(), (*updateCalls)[len(*updateCalls)-1])
	suite.Equal(SUCCEEDED.str(), *last.Status)
	suite.Require().NotEmpty(last.Artifact)
}

// --- failure paths (mirrors Test_ApplyTfFailure) -------------------------------------------------

func (suite *SingleRunWorkerTestSuite) Test_ExecuteRun_ApplyTfFailure() {
	w := suite.newWorker("single-run-token-apply-fail")
	run := suite.buildRun(withBehavior(APPLY.str()), withRepo(suite.repo.Path, suite.repoPath))

	suite.tfMock.applyFunc = func(ctx context.Context, opts ...tfexec.ApplyOption) error {
		_, _ = suite.tfMock.stdOut.Write([]byte("apply in progress\n"))
		_, _ = suite.tfMock.stdErr.Write([]byte("failure\n"))
		return errors.New("test error")
	}

	updateCalls := suite.collectUpdates()

	suite.Require().NoError(w.ExecuteRun(run), "ExecuteRun itself only errors on init/wd setup failures, not tf failures")

	suite.Require().GreaterOrEqual(len(*updateCalls), 1)
	last := decodeUpdate(suite.T(), (*updateCalls)[len(*updateCalls)-1])
	suite.Equal(FAILED.str(), *last.Status)
	suite.Len(last.Steps, 6)

	executeTf := findStep(suite.T(), last, StepExecuteTf)
	suite.Equal(FAILED.str(), *executeTf.Status)
	suite.NotNil(executeTf.UserMessage)
	suite.Equal("test error", *executeTf.UserMessage)
}

// --- runToken-only auth pin (D9 pin 16b) -----------------------------------------------------

// Test_ExecuteRun_RunTokenOnlyAuth_NeverBasic pins that every request SingleRunWorker's API client
// issues (registration, status updates, and the plan-artifact download) carries
// "Authorization: Bearer <runToken>" — the k8s single-run contract has no base-auth fallback
// configured (config.go's validateAuthConfig waives credentials entirely in this mode), so a
// regression that let a nil/empty auth header slip through would otherwise be invisible until a
// real meshfed API rejected the request.
func (suite *SingleRunWorkerTestSuite) Test_ExecuteRun_RunTokenOnlyAuth_NeverBasic() {
	const runToken = "single-run-token-auth-pin"
	w := suite.newWorker(runToken)

	planArtifactHref := "http://localhost/api/meshobjects/meshbuildingblockruns/run-uuid/plan-artifact"
	run := suite.buildRun(
		withBehavior(APPLY.str()),
		withRepo(suite.repo.Path, suite.repoPath),
		withPlanArtifact(planArtifactHref),
	)

	var authHeaders []string
	suite.calls.register = func(req *http.Request) *http.Response {
		authHeaders = append(authHeaders, req.Header.Get("Authorization"))
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBuffer(nil)), Header: make(http.Header)}
	}
	suite.calls.download = func(req *http.Request) *http.Response {
		authHeaders = append(authHeaders, req.Header.Get("Authorization"))
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBuffer([]byte("plan"))), Header: make(http.Header)}
	}
	suite.calls.update = func(req *http.Request) *http.Response {
		authHeaders = append(authHeaders, req.Header.Get("Authorization"))
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBuffer([]byte("{}"))), Header: make(http.Header)}
	}

	suite.Require().NoError(w.ExecuteRun(run))

	suite.Require().NotEmpty(authHeaders, "expected at least one authenticated request")
	for _, h := range authHeaders {
		suite.Equal("Bearer "+runToken, h)
		suite.NotContains(h, "Basic")
	}
}

// --- abort flag (mirrors Test_ApplyRunAborted) ----------------------------------------------------

func (suite *SingleRunWorkerTestSuite) Test_ExecuteRun_AbortFlagCancelsContext() {
	// simulate an init tf call that needs 11s to finish
	suite.tfMock.initFunc = func(ctx context.Context, opts ...tfexec.InitOption) error {
		time.Sleep(time.Second * 11)
		return nil
	}
	// apply must observe the context already cancelled by the time it would run
	suite.tfMock.applyFunc = func(ctx context.Context, opts ...tfexec.ApplyOption) error {
		suite.Equal(context.Canceled, ctx.Err())
		return nil
	}

	w := suite.newWorker("single-run-token-abort")
	run := suite.buildRun(withBehavior(APPLY.str()), withRepo(suite.repo.Path, suite.repoPath))

	updateCalls := make([]http.Request, 0)
	suite.calls.update = func(req *http.Request) *http.Response {
		updateCalls = append(updateCalls, *req)
		return mockUpdateCallWithAbortResponse()(req)
	}

	suite.Require().NoError(w.ExecuteRun(run))

	// only the first 10s-tick update should have fired (11s init duration / 10s update interval);
	// that single update carries the abort flag, cancelling the context before apply runs, and the
	// observer omits any further update once the context is Canceled (singlerunworker.go:155-158).
	suite.Len(updateCalls, 1)
	update := decodeUpdate(suite.T(), updateCalls[0])
	suite.Equal(IN_PROGRESS.str(), *update.Status)
}

// --- registration failure (mirrors singlerunworker.go:128-135) ------------------------------------

// Test_ExecuteRun_RegistrationFailure_ReportsFailed pins the fixed ExecuteRun (B11, phase 2b):
// registration is before tofu init/apply begins, so its failure is now propagated out of
// ExecuteRun as an error (unlike a later tf init/apply failure, which stays exit-0 — see
// singlerunworker.go's registerErr doc comment) — in addition to the pre-existing FAILED status
// report this test already pinned.
func (suite *SingleRunWorkerTestSuite) Test_ExecuteRun_RegistrationFailure_ReportsFailed() {
	w := suite.newWorker("single-run-token-register-fail")
	run := suite.buildRun(withBehavior(APPLY.str()), withRepo(suite.repo.Path, suite.repoPath))

	suite.calls.register = func(_ *http.Request) *http.Response {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(bytes.NewBuffer([]byte("boom"))),
			Header:     make(http.Header),
		}
	}

	initCalled := false
	suite.tfMock.initFunc = func(ctx context.Context, opts ...tfexec.InitOption) error {
		initCalled = true
		return nil
	}

	updateCalls := suite.collectUpdates()

	suite.Require().Error(w.ExecuteRun(run), "fixed: a registration failure now surfaces as an ExecuteRun error")

	suite.False(initCalled, "tf must never run when registration hard-fails")

	suite.Require().Len(*updateCalls, 1, "exactly one final update: registration failure short-circuits execute()")
	update := decodeUpdate(suite.T(), (*updateCalls)[0])
	suite.Equal(FAILED.str(), *update.Status)
	// registration failed before tfcmd.execute() ever called commitStatus, so the snapshot the
	// observer sends still carries the Steps == nil it was initialized with (runcontextinfo.go:39).
	suite.Empty(update.Steps)
}

// --- working-directory setup failures (singlerunworker.go:54-69) ---------------------------------

// Test_ExecuteRun_WorkerDirCreationFails_NoApiCall pins the *first* of ExecuteRun's two distinct
// failure branches: os.MkdirAll(w.workerDir, ...) failing (e.g. a path component that is a file,
// not a directory — as an unwritable/non-directory parent segment would look) returns an error
// straight away, WITHOUT calling sendInitFail. This differs from the second failure branch
// (Test_ExecuteRun_TempDirCreationFails_SendsInitFailUpdate below), which does call sendInitFail —
// pinned here verbatim (D13) rather than assumed.
func (suite *SingleRunWorkerTestSuite) Test_ExecuteRun_WorkerDirCreationFails_NoApiCall() {
	fileNotDir := filepath.Join(suite.T().TempDir(), "not-a-directory")
	suite.Require().NoError(os.WriteFile(fileNotDir, []byte("x"), 0600))

	w := suite.newWorker("single-run-token-mkdirall-fail")
	w.workerDir = filepath.Join(fileNotDir, "child") // MkdirAll must fail: fileNotDir isn't a dir

	var calledUpdate bool
	suite.calls.update = func(req *http.Request) *http.Response {
		calledUpdate = true
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBuffer([]byte("{}"))), Header: make(http.Header)}
	}
	suite.calls.register = func(req *http.Request) *http.Response {
		suite.Fail("registration must not be attempted when the working directory cannot be created")
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBuffer(nil)), Header: make(http.Header)}
	}

	run := suite.buildRun(withBehavior(APPLY.str()), withRepo(suite.repo.Path, suite.repoPath))
	err := w.ExecuteRun(run)

	suite.Require().Error(err)
	suite.Contains(err.Error(), "failed to create working directory")
	suite.False(calledUpdate, "no status update is sent when workerDir itself cannot be created")
}

// Test_ExecuteRun_TempDirCreationFails_SendsInitFailUpdate pins the second failure branch: workerDir
// itself already exists (so MkdirAll is a no-op success) but os.MkdirTemp underneath it fails (here,
// a read-only workerDir) — this path DOES call sendInitFail, reporting one FAILED update with no
// steps (singlerunworker.go:59-63, 201-214).
func (suite *SingleRunWorkerTestSuite) Test_ExecuteRun_TempDirCreationFails_SendsInitFailUpdate() {
	readOnlyDir := suite.T().TempDir()
	suite.Require().NoError(os.Chmod(readOnlyDir, 0500))
	suite.T().Cleanup(func() { _ = os.Chmod(readOnlyDir, 0700) }) // restore so TempDir's own cleanup can remove it

	w := suite.newWorker("single-run-token-mkdirtemp-fail")
	w.workerDir = readOnlyDir

	updateCalls := suite.collectUpdates()

	run := suite.buildRun(withBehavior(APPLY.str()), withRepo(suite.repo.Path, suite.repoPath))
	err := w.ExecuteRun(run)

	suite.Require().Error(err)
	suite.Contains(err.Error(), "failed to create temp directory")

	suite.Require().Len(*updateCalls, 1)
	update := decodeUpdate(suite.T(), (*updateCalls)[0])
	suite.Equal(FAILED.str(), *update.Status)
	suite.Empty(update.Steps)
	suite.Require().NotNil(update.Summary)
	suite.Equal("Something went wrong while starting the run.", *update.Summary)
}

// Test_ExecuteRun_CreatesWorkerDirIfMissing pins that ExecuteRun creates workerDir itself
// (os.MkdirAll, singlerunworker.go:54) rather than requiring the caller (main.go) to pre-create it —
// necessary because a fresh k8s single-run Job's working-dir mount may not exist yet.
func (suite *SingleRunWorkerTestSuite) Test_ExecuteRun_CreatesWorkerDirIfMissing() {
	notYetCreated := filepath.Join(suite.T().TempDir(), "does-not-exist-yet")

	w := suite.newWorker("single-run-token-mkdir-missing")
	w.workerDir = notYetCreated

	suite.collectUpdates()

	run := suite.buildRun(withBehavior(APPLY.str()), withRepo(suite.repo.Path, suite.repoPath))
	suite.Require().NoError(w.ExecuteRun(run))

	info, err := os.Stat(notYetCreated)
	suite.Require().NoError(err)
	suite.True(info.IsDir())
}

// --- saved-plan APPLY (mirrors Test_ApplyWithPlanArtifact_DownloadsAndAppliesSavedPlan) -----------

func (suite *SingleRunWorkerTestSuite) Test_ExecuteRun_AppliesSavedPlanArtifact() {
	savedPlanBytes := []byte("predecessor-saved-plan-binary-single-run")
	planArtifactHref := "http://localhost/api/meshobjects/meshbuildingblockruns/run-uuid/plan-artifact"

	const runToken = "single-run-token-saved-plan"
	w := suite.newWorker(runToken)
	run := suite.buildRun(
		withBehavior(APPLY.str()),
		withRepo(suite.repo.Path, suite.repoPath),
		withPlanArtifact(planArtifactHref),
	)

	downloadCalled := false
	suite.calls.download = func(req *http.Request) *http.Response {
		downloadCalled = true
		suite.Equal("application/octet-stream", req.Header.Get("Accept"))
		suite.Equal("Bearer "+runToken, req.Header.Get("Authorization"))
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

	updateCalls := suite.collectUpdates()

	suite.Require().NoError(w.ExecuteRun(run))

	suite.True(downloadCalled, "expected the predecessor plan artifact to be downloaded")
	suite.Equal(1, applyOptCount, "expected apply to be called with a single (DirOrPlan) option")
	suite.Equal(savedPlanBytes, planOnDisk)

	suite.Require().GreaterOrEqual(len(*updateCalls), 1)
	last := decodeUpdate(suite.T(), (*updateCalls)[len(*updateCalls)-1])
	suite.Equal(SUCCEEDED.str(), *last.Status)
}

// --- constructor pins (D9 pin 3: 10s status ticker) ------------------------------------------------

// Test_NewSingleRunWorker_SetsDefaults pins NewSingleRunWorker's fixed statusUpdateInterval (10s,
// D9 pin 3) and its timeout-from-minutes conversion.
func Test_NewSingleRunWorker_SetsDefaults(t *testing.T) {
	previous := AppConfig
	AppConfig = TfRunnerConfig{RunnerUuid: "ctor-pin-runner"}
	t.Cleanup(func() { AppConfig = previous })

	logger := log.New(io.Discard, "", 0)
	w := NewSingleRunWorker(logger, t.TempDir(), 7, nil, nil)

	if w.statusUpdateInterval != 10*time.Second {
		t.Errorf("statusUpdateInterval = %v, want 10s", w.statusUpdateInterval)
	}
	if w.timeout != 7*time.Minute {
		t.Errorf("timeout = %v, want 7m", w.timeout)
	}
	if w.runApi == nil {
		t.Error("runApi is nil, want NewRunApi() result")
	}
}

// Test_NewSingleRunWorkerWithApi_SetsDefaults pins the same statusUpdateInterval/timeout defaults
// for the constructor used by the k8s single-run entrypoint, which supplies its own runToken-aware
// RunApi instance rather than letting the constructor build one from AppConfig.
func Test_NewSingleRunWorkerWithApi_SetsDefaults(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	api := &RunApiClient{}
	w := NewSingleRunWorkerWithApi(logger, t.TempDir(), 3, nil, api, nil)

	if w.statusUpdateInterval != 10*time.Second {
		t.Errorf("statusUpdateInterval = %v, want 10s", w.statusUpdateInterval)
	}
	if w.timeout != 3*time.Minute {
		t.Errorf("timeout = %v, want 3m", w.timeout)
	}
	if w.runApi != RunApi(api) {
		t.Error("runApi is not the provided instance")
	}
}
