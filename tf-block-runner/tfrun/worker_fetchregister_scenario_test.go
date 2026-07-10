package tfrun

// CP2 (PLAN_DETAIL_01_tf_characterization_tests.md §9): Worker fetch/register failure paths.
// Kept in its own file (disjoint from worker_scenario_test.go, which other checkpoints extend)
// so this checkpoint lands independently; it reuses WorkerTestSuite/fixtures from CP1
// (worker_scenario_test.go, fixtures_test.go) rather than duplicating suite setup.

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
	"sync"

	"github.com/hashicorp/terraform-exec/tfexec"
	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
)

// errTransport simulates a network/transport-level failure (as opposed to an HTTP status
// response): it always returns a non-nil error from RoundTrip, the shape needed to pin
// worker.go:84's transport-error branches. The suite's existing testRoundTripper
// (worker_scenario_test.go) cannot produce this — it always returns a *http.Response with a nil
// error — because suite.calls.fetch's signature is response-only.
type errTransport struct{ err error }

func (t errTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, t.err
}

// fixedStatusResponse builds a suite.calls.fetch-compatible handler that answers every request
// with the given HTTP status and an empty (but non-nil) body, so meshapi.Client's
// `defer resp.Body.Close()` has something to close on the error path.
func fixedStatusResponse(status int) func(*http.Request) *http.Response {
	return func(*http.Request) *http.Response {
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(bytes.NewReader(nil)),
			Header:     make(http.Header),
		}
	}
}

// Test_HandleFetchRunError pins D9 pin 6 (404/409/500 on claim) and the worker.go:81-89 transport-
// error heuristic (the double-Transfer-Encoding mux quirk vs. any other transport error), driving
// each case black-box through a fresh Worker.work() (the survival contract for phase 2) rather than
// calling the unexported handleFetchRunError directly.
func (suite *WorkerTestSuite) Test_HandleFetchRunError() {
	chunkedErr := errors.New(`transport connection broken: too many transfer encodings: ["chunked" "chunked"]`)
	genericTransportErr := errors.New("connection reset by peer")

	cases := []struct {
		name      string
		transport http.RoundTripper
		want      workerToken
	}{
		{"404 not found -> norun (worker.go:66-91)", testRoundTripper(fixedStatusResponse(404)), norun},
		{"409 conflict -> norun", testRoundTripper(fixedStatusResponse(409)), norun},
		{"500 server error -> failed", testRoundTripper(fixedStatusResponse(500)), failed},
		{"double-chunked transport error -> norun (worker.go:84 string pin)", errTransport{chunkedErr}, norun},
		{"other transport error -> failed", errTransport{genericTransportErr}, failed},
	}

	for _, tc := range cases {
		suite.Run(tc.name, func() {
			auth := &runApiAuth{baseAuth: meshapi.BasicAuth{Username: "u", Password: "p"}}
			w := &Worker{
				workerNumber: 1,
				workerIn:     make(chan workerToken, 2),
				workerOut:    make(chan workerToken, 2),
				runApi:       newScenarioRunApiClient("cp2-fetcherr", auth, tc.transport, NoopDecryptor{}),
				log:          log.New(io.Discard, "", 0),
			}

			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				w.work()
				wg.Done()
			}()
			w.workerIn <- work
			w.workerIn <- stop
			wg.Wait()

			suite.Equal(tc.want, <-w.workerOut, "result token for %s", tc.name)
			suite.Equal(stopped, <-w.workerOut, "stop token for %s", tc.name)
		})
	}
}

// Test_RegistrationHardFailure_ReportsFailedWithoutExecutingTf pins the use-case matrix row
// "registration hard failure (500) -> FAILED final, tf never runs" and worker.go:170-181: when
// Register returns an error, workRoutine marks the run FAILED and returns without ever calling
// tfCommand.execute(), so Init/Apply must never be observed.
func (suite *WorkerTestSuite) Test_RegistrationHardFailure_ReportsFailedWithoutExecutingTf() {
	suite.calls.fetch = mockValidRunDetailsFetchCall(APPLY.str(), suite.repo.Path, suite.repoPath)
	suite.calls.register = func(*http.Request) *http.Response {
		return &http.Response{
			StatusCode: 500,
			Body:       io.NopCloser(bytes.NewBufferString("registration failed")),
			Header:     make(http.Header),
		}
	}

	var initCalled, applyCalled bool
	suite.tfMock.initFunc = func(ctx context.Context, opts ...tfexec.InitOption) error {
		initCalled = true
		return nil
	}
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

	suite.False(initCalled, "Init must not run once registration has failed (worker.go:170-181)")
	suite.False(applyCalled, "Apply must not run once registration has failed")
	suite.Len(updateCalls, 1, "exactly one final status update is expected")

	data, _ := io.ReadAll(updateCalls[0].Body)
	var update meshapi.RunStatusUpdateDTO
	json.Unmarshal(data, &update)
	suite.Equal(FAILED.str(), *update.Status)
}

// Test_SendInitFail_WhenWorkerDirIsUnusable pins worker.go:96-101/251-264: when the per-run
// working directory cannot be created, the worker reports one FAILED update with a fixed summary
// and no steps, instead of ever registering or executing tf.
func (suite *WorkerTestSuite) Test_SendInitFail_WhenWorkerDirIsUnusable() {
	suite.calls.fetch = mockValidRunDetailsFetchCall(APPLY.str(), suite.repo.Path, suite.repoPath)

	// os.MkdirTemp requires its first argument to be an existing directory; pointing workerDir at
	// a regular file forces tfExecution's MkdirTemp call to fail without touching the filesystem's
	// permissions (which root/CI can bypass).
	notADir := filepath.Join(suite.T().TempDir(), "not-a-directory")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		suite.T().Fatalf("fixture setup: writing %q: %v", notADir, err)
	}
	suite.w.workerDir = notADir

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

	suite.Len(updateCalls, 1, "exactly one FAILED update is expected on init failure")

	data, _ := io.ReadAll(updateCalls[0].Body)
	var update meshapi.RunStatusUpdateDTO
	json.Unmarshal(data, &update)
	suite.Equal(FAILED.str(), *update.Status)
	suite.Require().NotNil(update.Summary)
	suite.Equal("Something went wrong while starting the run.", *update.Summary)
	suite.Nil(update.Steps)
}
