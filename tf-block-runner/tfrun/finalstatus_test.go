package tfrun

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
)

var shortRetry = retrySchedule{initialDelay: time.Millisecond, maxDelay: 5 * time.Millisecond, giveUpAfter: 200 * time.Millisecond}

func runApiAnswering(t *testing.T, statuses ...int) (RunApi, *int) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status := statuses[min(calls, len(statuses)-1)]
		calls++
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
	assert.Equal(t, 3, *calls)
}

func TestUpdateStateWithRetry_DoesNotRetryAClientError(t *testing.T) {
	api, calls := runApiAnswering(t, http.StatusForbidden)

	err := updateStateWithRetry(api, &RunStatus{RunId: "run", Status: FAILED}, shortRetry, log.New(io.Discard, "", 0))

	assert.Error(t, err)
	assert.Equal(t, 1, *calls)
}

func TestUpdateStateWithRetry_GivesUpWhileMeshfedStaysDown(t *testing.T) {
	api, calls := runApiAnswering(t, http.StatusServiceUnavailable)

	err := updateStateWithRetry(api, &RunStatus{RunId: "run", Status: FAILED}, shortRetry, log.New(io.Discard, "", 0))

	assert.ErrorContains(t, err, "503")
	assert.Greater(t, *calls, 1)
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
