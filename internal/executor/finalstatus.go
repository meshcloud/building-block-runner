package executor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"time"
	"uuid"

	"github.com/cenkalti/backoff/v5"
	"github.com/meshcloud/meshstack-cli/client"
)

const statusRequestTimeout = 30 * time.Second

type retrySchedule struct {
	initialDelay   time.Duration
	maxDelay       time.Duration
	attemptTimeout time.Duration
	giveUpAfter    time.Duration
}

// finalStatusRetry outlasts a meshfed deployment, during which meshfed answers 503 for about two minutes.
var finalStatusRetry = retrySchedule{
	initialDelay:   time.Second,
	maxDelay:       30 * time.Second,
	attemptTimeout: statusRequestTimeout,
	giveUpAfter:    10 * time.Minute,
}

// patchWithRetry sends an update that no later update repeats; meshfed shows a lost one as in progress for hours.
func patchWithRetry(ctx context.Context, api client.MeshRunnerRunClient, sourceId uuid.UUID, update client.MeshRunnerRunStatusUpdate) error {
	schedule := finalStatusRetry
	ctx, cancel := context.WithTimeoutCause(ctx, schedule.giveUpAfter,
		fmt.Errorf("meshfed did not take the status within %s: %w", schedule.giveUpAfter, context.DeadlineExceeded))
	defer cancel()

	retried := false
	_, err := backoff.Retry(ctx,
		func() (struct{}, error) {
			_, err := patchWithin(ctx, api, sourceId, update, schedule.attemptTimeout)
			switch {
			case err == nil:
			case isTransient(err):
				retried = true
			case retried && isClientError(err):
				err = backoff.Permanent(fmt.Errorf("meshfed rejected a retry, so an earlier attempt may have delivered the status: %w", err))
			default:
				err = backoff.Permanent(err)
			}
			return struct{}{}, err
		},
		backoff.WithBackOff(&backoff.ExponentialBackOff{
			InitialInterval:     schedule.initialDelay,
			MaxInterval:         schedule.maxDelay,
			Multiplier:          2,
			RandomizationFactor: backoff.DefaultRandomizationFactor,
		}),
		// 0 turns off the library's own 15-minute cap, so the context alone ends the retry.
		backoff.WithMaxElapsedTime(0),
		backoff.WithNotify(func(err error, delay time.Duration) {
			slog.WarnContext(ctx, "status update failed, retrying", "retryIn", delay, "error", err)
		}),
	)
	return err
}

func patchWithin(ctx context.Context, api client.MeshRunnerRunClient, sourceId uuid.UUID, update client.MeshRunnerRunStatusUpdate, timeout time.Duration) (client.MeshRunnerRunStatusUpdateResult, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return api.PatchStatus(ctx, sourceId, update)
}

// isTransient includes 423, which meshfed answers while another change holds the run's lock. It leaves out 500, as a
// retry rarely passes and holds the runner for ten minutes.
func isTransient(err error) bool {
	if httpErr, ok := errors.AsType[client.HttpError](err); ok {
		return slices.Contains([]int{http.StatusLocked, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout}, httpErr.StatusCode)
	}
	_, isTransportErr := errors.AsType[*url.Error](err)
	return isTransportErr
}

func isClientError(err error) bool {
	httpErr, ok := errors.AsType[client.HttpError](err)
	return ok && httpErr.StatusCode >= 400 && httpErr.StatusCode < 500
}
