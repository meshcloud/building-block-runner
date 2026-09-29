package tfrun

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
)

var shortRetry = retrySchedule{initialDelay: time.Millisecond, maxDelay: 5 * time.Millisecond, attemptTimeout: 50 * time.Millisecond, giveUpAfter: 200 * time.Millisecond}

func runApiAnswering(t *testing.T, statuses ...int) (RunApi, *atomic.Int32) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := int(calls.Add(1))
		status := statuses[min(call, len(statuses))-1]
		w.WriteHeader(status)
		w.Write([]byte("{}"))
	}))
	t.Cleanup(server.Close)

	auth := &runApiAuth{baseAuth: meshapi.BasicAuth{Username: "test-user", Password: "test-pass"}}
	return &RunApiClient{auth: auth, client: meshapi.NewClient(server.URL, "test-runner", auth)}, &calls
}

func TestUpdateStateWithRetry_DeliversOnceMeshfedIsBackAfter503(t *testing.T) {
	api, calls := runApiAnswering(t, http.StatusServiceUnavailable, http.StatusServiceUnavailable, http.StatusOK)

	err := updateStateWithRetry(api, &RunStatus{RunId: "run", Status: FAILED}, shortRetry, log.New(io.Discard, "", 0))

	assert.NoError(t, err)
	assert.Equal(t, int32(3), calls.Load())
}

func TestUpdateStateWithRetry_DoesNotRetryAClientError(t *testing.T) {
	api, calls := runApiAnswering(t, http.StatusForbidden)

	err := updateStateWithRetry(api, &RunStatus{RunId: "run", Status: FAILED}, shortRetry, log.New(io.Discard, "", 0))

	assert.ErrorContains(t, err, "403")
	assert.NotContains(t, err.Error(), "earlier attempt")
	assert.Equal(t, int32(1), calls.Load())
}

func TestUpdateStateWithRetry_DoesNotRetryAnInternalServerError(t *testing.T) {
	api, calls := runApiAnswering(t, http.StatusInternalServerError)

	err := updateStateWithRetry(api, &RunStatus{RunId: "run", Status: FAILED}, shortRetry, log.New(io.Discard, "", 0))

	assert.ErrorContains(t, err, "500")
	assert.Equal(t, int32(1), calls.Load())
}

func TestUpdateStateWithRetry_StopsAtAnInternalServerErrorAfter503(t *testing.T) {
	api, calls := runApiAnswering(t, http.StatusServiceUnavailable, http.StatusInternalServerError)

	err := updateStateWithRetry(api, &RunStatus{RunId: "run", Status: FAILED}, shortRetry, log.New(io.Discard, "", 0))

	assert.ErrorContains(t, err, "500")
	assert.NotContains(t, err.Error(), "earlier attempt")
	assert.Equal(t, int32(2), calls.Load())
}

func TestUpdateStateWithRetry_SaysAnEarlierAttemptMayHaveDeliveredWhenARetryIsRejected(t *testing.T) {
	api, calls := runApiAnswering(t, http.StatusGatewayTimeout, http.StatusUnauthorized)

	err := updateStateWithRetry(api, &RunStatus{RunId: "run", Status: FAILED}, shortRetry, log.New(io.Discard, "", 0))

	assert.ErrorContains(t, err, "an earlier attempt may have delivered the status")
	assert.Equal(t, int32(2), calls.Load())
}

func TestUpdateStateWithRetry_GivesUpWhileMeshfedStaysDown(t *testing.T) {
	api, calls := runApiAnswering(t, http.StatusServiceUnavailable)

	err := updateStateWithRetry(api, &RunStatus{RunId: "run", Status: FAILED}, shortRetry, log.New(io.Discard, "", 0))

	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Greater(t, calls.Load(), int32(1))
}

func TestUpdateStateWithRetry_RetriesWhenMeshfedIsUnreachable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close()
	auth := &runApiAuth{}
	api := &RunApiClient{auth: auth, client: meshapi.NewClient(server.URL, "test-runner", auth)}

	start := time.Now()
	err := updateStateWithRetry(api, &RunStatus{RunId: "run", Status: FAILED}, shortRetry, log.New(io.Discard, "", 0))

	assert.Error(t, err)
	assert.GreaterOrEqual(t, time.Since(start), shortRetry.giveUpAfter-shortRetry.maxDelay)
}

func TestUpdateStateWithRetry_RetriesAnAttemptThatGetsNoAnswer(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			stallUntilTestEnds(t)
			return
		}
		w.Write([]byte("{}"))
	}))
	t.Cleanup(server.Close)
	auth := &runApiAuth{}
	api := &RunApiClient{auth: auth, client: meshapi.NewClient(server.URL, "test-runner", auth)}

	err := updateStateWithRetry(api, &RunStatus{RunId: "run", Status: FAILED}, shortRetry, log.New(io.Discard, "", 0))

	assert.NoError(t, err)
	assert.Equal(t, int32(2), calls.Load())
}

func TestUpdateStateWithRetry_GivesUpInTimeWhenNoAttemptGetsAnAnswer(t *testing.T) {
	schedule := shortRetry
	schedule.attemptTimeout = time.Hour
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stallUntilTestEnds(t)
	}))
	t.Cleanup(server.Close)
	auth := &runApiAuth{}
	api := &RunApiClient{auth: auth, client: meshapi.NewClient(server.URL, "test-runner", auth)}

	start := time.Now()
	err := updateStateWithRetry(api, &RunStatus{RunId: "run", Status: FAILED}, schedule, log.New(io.Discard, "", 0))

	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 2*schedule.giveUpAfter)
}

// stallUntilTestEnds holds a request unanswered. The request context is no signal to stop: the server notices a
// client that gave up only after the handler read the request body.
func stallUntilTestEnds(t *testing.T) {
	testEnded := make(chan struct{})
	t.Cleanup(func() { close(testEnded) })
	<-testEnded
}
