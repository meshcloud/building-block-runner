package tf

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/stretchr/testify/suite"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/report"
)

// HandlerTestSuite drives the tf dispatch.RunHandler (Handler.Execute) end to end through the
// SAME hermetic fixtures the polling Worker suite uses (MockedTfFacade, local git repo, fake
// RoundTripper), proving the handler path produces the same register/PATCH/metering behavior --
// this is the equivalence evidence needed at the handler seam.
type HandlerTestSuite struct {
	suite.Suite
	tfBin    *TfBinaries
	tfMock   *MockedTfFacade
	meter    *fakeMeter
	repo     *localGitRepo
	repoPath string
	// cfg is the runner config threaded into the Handler under test, replacing
	// the former package-level AppConfig global.
	cfg TfRunnerConfig

	// mutable per-test routing for the run-scoped RunApi's fake transport.
	register func(*http.Request) *http.Response
	update   func(*http.Request) *http.Response
	download func(*http.Request) *http.Response
}

func Test_HandlerSuite(t *testing.T) {
	suite.Run(t, new(HandlerTestSuite))
}

func (s *HandlerTestSuite) SetupSuite() {
	testTfInstallDir, err := os.MkdirTemp(os.TempDir(), "handlerScenario-tf-")
	s.Require().NoError(err)
	tmpWd, err := os.MkdirTemp(os.TempDir(), "handlerScenario-wd-")
	s.Require().NoError(err)

	s.cfg = TfRunnerConfig{
		RunnerUuid:           "scenario-runner",
		InitTimeoutMins:      10,
		WsTimeoutMins:        10,
		TfCommandTimeoutMins: 10,
		TfParentWorkingDir:   tmpWd,
	}

	s.tfMock = &MockedTfFacade{}
	s.tfMock.initMockFuncs()
	s.tfBin, err = ForTestNewTfBin(testTfInstallDir, io.Discard, s.tfMock)
	s.Require().NoError(err)

	s.repoPath = "modules/github/repository/buildingblock"
	s.repo = makeLocalGitRepo(s.T(), map[string]string{
		s.repoPath + "/main.tf": "# fixture terraform source, not executed (MockedTfFacade stubs tf calls)\n",
	})
}

func (s *HandlerTestSuite) TearDownSuite() {
	_ = os.RemoveAll(s.tfBin.dir)
	_ = os.RemoveAll(s.cfg.TfParentWorkingDir)
}

func (s *HandlerTestSuite) SetupTest() {
	s.tfMock.initMockFuncs()
	s.meter = &fakeMeter{}
	s.register = noopCall
	s.update = noopCall
	s.download = noopCall
}

func (s *HandlerTestSuite) route(req *http.Request) *http.Response {
	switch {
	case req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/plan-artifact"):
		return s.download(req)
	case req.Method == http.MethodPatch && strings.Contains(req.URL.Path, "/status/source"):
		return s.update(req)
	case req.Method == http.MethodPost && strings.Contains(req.URL.Path, "/status/source"):
		return s.register(req)
	default:
		return noopCall(req)
	}
}

// newHandler builds a Handler whose per-run RunApi is a scenario client over s.route, and
// asserts the run token wired into it (run-scoped, runToken-only auth).
func (s *HandlerTestSuite) newHandler(wantToken string) Handler {
	return NewHandler(HandlerConfig{
		WorkingDir:       s.cfg.TfParentWorkingDir,
		TfCommandTimeout: 30 * time.Minute,
		InitTimeout:      time.Duration(s.cfg.InitTimeoutMins) * time.Minute,
		WsTimeout:        time.Duration(s.cfg.WsTimeoutMins) * time.Minute,
		RunnerUuid:       s.cfg.RunnerUuid,
	}, HandlerDeps{
		TfBinaries: s.tfBin,
		Meter:      s.meter,
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		NewRunApi: func(runToken string) RunApi {
			s.Equal(wantToken, runToken, "handler must build its RunApi with the run's own runToken")
			auth := &runApiAuth{runToken: &runToken, baseAuth: meshapi.BasicAuth{Username: "u", Password: "p"}}
			return newScenarioRunApiClient("scenario-runner", auth, testRoundTripper(s.route))
		},
	})
}

func (s *HandlerTestSuite) claimedRun(behavior string) dispatch.ClaimedRun {
	dto := runDetailsDTO(withBehavior(behavior), withRepo(s.repo.Path, s.repoPath), withRunToken("run-token-xyz"))
	raw, err := json.Marshal(dto)
	s.Require().NoError(err)
	return dispatch.ClaimedRun{
		Id:      dispatch.RunId(dto.Metadata.Uuid),
		Details: dto,
		RawJson: base64.StdEncoding.EncodeToString(raw),
	}
}

func (s *HandlerTestSuite) Test_ApplySucceeded() {
	var mu sync.Mutex
	updates := make([]meshapi.RunStatusUpdateDTO, 0)
	s.update = func(req *http.Request) *http.Response {
		data, _ := io.ReadAll(req.Body)
		var u meshapi.RunStatusUpdateDTO
		s.Require().NoError(json.Unmarshal(data, &u))
		mu.Lock()
		updates = append(updates, u)
		mu.Unlock()
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBufferString("{}")), Header: make(http.Header)}
	}

	err := s.newHandler("run-token-xyz").Execute(context.Background(), s.claimedRun(APPLY.str()))
	s.Require().NoError(err)

	s.Require().GreaterOrEqual(len(updates), 1)
	final := updates[len(updates)-1]
	s.Equal(report.SUCCEEDED.String(), *final.Status)
	// The run-scoped PATCH must be stamped with the runner uuid as source (frozen wire shape).
	s.Equal("scenario-runner", final.Source)

	s.Equal(meterCounts{claimed: 1, succeeded: 1, failed: 0, pollErrors: 0}, s.meter.snapshot())
}

func (s *HandlerTestSuite) Test_ApplyTfFailure_ReportsFailedAndMeters() {
	s.tfMock.applyFunc = func(_ context.Context, _ ...tfexec.ApplyOption) error {
		_, _ = s.tfMock.stdErr.Write([]byte("boom\n"))
		return errors.New("tf apply error")
	}

	var mu sync.Mutex
	var lastStatus string
	s.update = func(req *http.Request) *http.Response {
		data, _ := io.ReadAll(req.Body)
		var u meshapi.RunStatusUpdateDTO
		s.Require().NoError(json.Unmarshal(data, &u))
		if u.Status != nil {
			mu.Lock()
			lastStatus = *u.Status
			mu.Unlock()
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBufferString("{}")), Header: make(http.Header)}
	}

	err := s.newHandler("run-token-xyz").Execute(context.Background(), s.claimedRun(APPLY.str()))
	s.Require().NoError(err) // run-level failures are reported by the handler, not returned

	s.Equal(report.FAILED.String(), lastStatus)
	s.Equal(meterCounts{claimed: 1, succeeded: 0, failed: 1, pollErrors: 0}, s.meter.snapshot())
}

// Test_RegisterHardFail_ReturnsErrorReportsFailedAndMeters pins Worker.workRoutine's registerErr
// surfacing through the Handler seam: a 500 from the registration POST must make Execute return a
// non-nil error (worker.go:170's registerErr return), while still reporting the run terminal
// FAILED and metering it as claimed+failed -- registration happens before tofu init/apply begins,
// so it is the one workRoutine failure Handler.Execute's caller must be able to observe.
func (s *HandlerTestSuite) Test_RegisterHardFail_ReturnsErrorReportsFailedAndMeters() {
	s.register = func(req *http.Request) *http.Response {
		return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(bytes.NewBufferString("boom")), Header: make(http.Header)}
	}

	var mu sync.Mutex
	var lastStatus string
	s.update = func(req *http.Request) *http.Response {
		data, _ := io.ReadAll(req.Body)
		var u meshapi.RunStatusUpdateDTO
		s.Require().NoError(json.Unmarshal(data, &u))
		if u.Status != nil {
			mu.Lock()
			lastStatus = *u.Status
			mu.Unlock()
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBufferString("{}")), Header: make(http.Header)}
	}

	err := s.newHandler("run-token-xyz").Execute(context.Background(), s.claimedRun(APPLY.str()))
	s.Require().Error(err, "a hard registration failure must surface out of Execute")

	s.Equal(report.FAILED.String(), lastStatus)
	s.Equal(meterCounts{claimed: 1, succeeded: 0, failed: 1, pollErrors: 0}, s.meter.snapshot())
}

// Test_PreFlightWorkdirFailure_ReturnsErrorAndSendsInitFail pins tfExecution's pre-flight
// os.MkdirTemp failure path (worker.go:62-67) through the Handler seam: a WorkingDir that is not
// itself a directory makes os.MkdirTemp fail before any tofu command or registration is attempted,
// Execute returns a non-nil error, and sendInitFail's single FAILED update (no steps) is sent.
func (s *HandlerTestSuite) Test_PreFlightWorkdirFailure_ReturnsErrorAndSendsInitFail() {
	notADir := s.T().TempDir() + "/not-a-directory"
	s.Require().NoError(os.WriteFile(notADir, []byte("x"), 0600))

	var mu sync.Mutex
	updates := make([]meshapi.RunStatusUpdateDTO, 0)
	s.update = func(req *http.Request) *http.Response {
		data, _ := io.ReadAll(req.Body)
		var u meshapi.RunStatusUpdateDTO
		s.Require().NoError(json.Unmarshal(data, &u))
		mu.Lock()
		updates = append(updates, u)
		mu.Unlock()
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBufferString("{}")), Header: make(http.Header)}
	}
	s.register = func(req *http.Request) *http.Response {
		s.Fail("registration must not be attempted when the working directory cannot be created")
		return noopCall(req)
	}

	badHandler := NewHandler(HandlerConfig{
		WorkingDir:       notADir,
		TfCommandTimeout: 30 * time.Minute,
		InitTimeout:      time.Duration(s.cfg.InitTimeoutMins) * time.Minute,
		WsTimeout:        time.Duration(s.cfg.WsTimeoutMins) * time.Minute,
		RunnerUuid:       s.cfg.RunnerUuid,
	}, HandlerDeps{
		TfBinaries: s.tfBin,
		Meter:      s.meter,
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		NewRunApi: func(runToken string) RunApi {
			auth := &runApiAuth{runToken: &runToken, baseAuth: meshapi.BasicAuth{Username: "u", Password: "p"}}
			return newScenarioRunApiClient("scenario-runner", auth, testRoundTripper(s.route))
		},
	})

	err := badHandler.Execute(context.Background(), s.claimedRun(APPLY.str()))
	s.Require().Error(err)

	s.Require().Len(updates, 1, "sendInitFail sends exactly one FAILED update, no steps")
	s.Equal(report.FAILED.String(), *updates[0].Status)
	s.Empty(updates[0].Steps)
	s.Equal(meterCounts{claimed: 1, succeeded: 0, failed: 1, pollErrors: 0}, s.meter.snapshot())
}

// Test_MappingFailure_ReturnsErrorWithoutReporting pins the historic polling behavior: a run
// whose details cannot be mapped (here, an unrecognized behavior) surfaces as an out-of-band
// error with NO status report -- exactly as the old FetchRunDetails path handled a mapping
// failure (backoff, coordinator times it out); RunClaimed is not metered (worker.go parity: it
// fires only after a successful fetch+map).
func (s *HandlerTestSuite) Test_MappingFailure_ReturnsErrorWithoutReporting() {
	reported := false
	s.update = func(req *http.Request) *http.Response {
		reported = true
		return noopCall(req)
	}
	s.register = func(req *http.Request) *http.Response {
		reported = true
		return noopCall(req)
	}

	dto := runDetailsDTO(withBehavior("BOGUS_BEHAVIOR"), withRepo(s.repo.Path, s.repoPath))
	raw, _ := json.Marshal(dto)
	cr := dispatch.ClaimedRun{Id: dispatch.RunId(dto.Metadata.Uuid), Details: dto, RawJson: base64.StdEncoding.EncodeToString(raw)}

	err := s.newHandler("").Execute(context.Background(), cr)
	s.Require().Error(err)
	s.False(reported, "a mapping failure must not report any status (historic polling behavior)")
	// Not metered as claimed: RunClaimed only fires after a successful mapping (worker.go parity).
	s.Equal(meterCounts{}, s.meter.snapshot())
}
