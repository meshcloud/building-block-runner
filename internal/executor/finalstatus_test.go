package executor

import (
	"context"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"testing/synctest"
	"time"
	"uuid"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var failed = client.MeshRunnerRunStatusUpdate{Status: new(string(StatusFailed))}

// stall stands for an attempt that gets no answer until its timeout.
const stall = -1

type answeringRunApi struct {
	client.MeshRunnerRunClient

	mu      sync.Mutex
	answers []int
	calls   int
}

func (a *answeringRunApi) PatchStatus(ctx context.Context, _ uuid.UUID, _ client.MeshRunnerRunStatusUpdate) (client.MeshRunnerRunStatusUpdateResult, error) {
	a.mu.Lock()
	a.calls++
	answer := a.answers[min(a.calls, len(a.answers))-1]
	a.mu.Unlock()
	switch answer {
	case stall:
		<-ctx.Done()
		return client.MeshRunnerRunStatusUpdateResult{}, &url.Error{Op: http.MethodPatch, URL: "/source", Err: ctx.Err()}
	case http.StatusOK:
		return client.MeshRunnerRunStatusUpdateResult{}, nil
	}
	return client.MeshRunnerRunStatusUpdateResult{}, client.HttpError{StatusCode: answer}
}

func answerName(answer int) string {
	if answer == stall {
		return "no answer"
	}
	return http.StatusText(answer)
}

func patchAnswered(t *testing.T, answers ...int) (calls int, took time.Duration, err error) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		api := &answeringRunApi{answers: answers}
		start := time.Now()
		err = patchWithRetry(t.Context(), api, runnerUuid, failed)
		calls, took = api.calls, time.Since(start)
	})
	return
}

func Test_PatchWithRetry_DeliversOnceMeshfedIsBackAfterATransientStatus(t *testing.T) {
	for _, transient := range []int{http.StatusLocked, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout, stall} {
		t.Run(answerName(transient), func(t *testing.T) {
			calls, _, err := patchAnswered(t, transient, transient, http.StatusOK)

			require.NoError(t, err)
			assert.Equal(t, 3, calls)
		})
	}
}

func Test_PatchWithRetry_DoesNotRetryAClientError(t *testing.T) {
	calls, _, err := patchAnswered(t, http.StatusForbidden)

	require.ErrorContains(t, err, "403")
	assert.NotContains(t, err.Error(), "earlier attempt")
	assert.Equal(t, 1, calls)
}

func Test_PatchWithRetry_DoesNotRetryAnInternalServerError(t *testing.T) {
	calls, _, err := patchAnswered(t, http.StatusInternalServerError)

	require.ErrorContains(t, err, "500")
	assert.Equal(t, 1, calls)
}

func Test_PatchWithRetry_StopsAtAnInternalServerErrorAfter503(t *testing.T) {
	calls, _, err := patchAnswered(t, http.StatusServiceUnavailable, http.StatusInternalServerError)

	require.ErrorContains(t, err, "500")
	assert.NotContains(t, err.Error(), "earlier attempt")
	assert.Equal(t, 2, calls)
}

func Test_PatchWithRetry_SaysAnEarlierAttemptMayHaveDeliveredWhenARetryIsRejected(t *testing.T) {
	calls, _, err := patchAnswered(t, http.StatusGatewayTimeout, http.StatusUnauthorized)

	require.ErrorContains(t, err, "an earlier attempt may have delivered the status")
	assert.Equal(t, 2, calls)
}

func Test_PatchWithRetry_GivesUpInTimeWhileMeshfedStaysDown(t *testing.T) {
	for _, down := range []int{http.StatusServiceUnavailable, stall} {
		t.Run(answerName(down), func(t *testing.T) {
			calls, took, err := patchAnswered(t, down)

			require.ErrorIs(t, err, context.DeadlineExceeded)
			assert.Greater(t, calls, 1)
			assert.LessOrEqual(t, took, finalStatusRetry.giveUpAfter)
		})
	}
}
