package tfrun

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/cenkalti/backoff/v5"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
)

const statusRequestTimeout = 30 * time.Second

type retrySchedule struct {
	initialDelay   time.Duration
	maxDelay       time.Duration
	attemptTimeout time.Duration
	giveUpAfter    time.Duration
}

// finalStatusRetry outlasts a meshfed deployment: its only pod is replaced, and meshfed answers 503 for
// about two minutes.
var finalStatusRetry = retrySchedule{
	initialDelay:   time.Second,
	maxDelay:       30 * time.Second,
	attemptTimeout: statusRequestTimeout,
	giveUpAfter:    10 * time.Minute,
}

// updateStateWithRetry sends a status update that no later update repeats. If it is lost, meshfed shows
// the run as in progress until its stuck-run timeout fails it, hours later.
func updateStateWithRetry(api RunApi, status *RunStatus, schedule retrySchedule, logger *log.Logger) error {
	ctx, cancel := context.WithTimeoutCause(context.Background(), schedule.giveUpAfter,
		fmt.Errorf("meshfed did not take the status within %s: %w", schedule.giveUpAfter, context.DeadlineExceeded))
	defer cancel()

	retried := false
	_, err := backoff.Retry(ctx,
		func() (struct{}, error) {
			_, err := updateStateWithin(ctx, api, status, schedule.attemptTimeout)
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
			logger.Printf("Status update for run %s failed, retrying in %s: %v", status.RunId, delay, err)
		}),
	)
	return err
}

func updateStateWithin(ctx context.Context, api RunApi, status *RunStatus, timeout time.Duration) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return api.UpdateState(ctx, status)
}

// isTransient includes 423, which meshfed answers while another change to the run holds its lock. It leaves out 500:
// it is unlikely to pass on a retry, and a retry holds one of the few runners for ten minutes, so a run that always
// fails with 500 could keep every runner from starting other runs.
func isTransient(err error) bool {
	if statusErr, ok := errors.AsType[*meshapi.StatusError](err); ok {
		return slices.Contains([]int{http.StatusLocked, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout}, statusErr.Status)
	}
	_, isTransportErr := errors.AsType[*url.Error](err)
	return isTransportErr
}

func isClientError(err error) bool {
	statusErr, ok := errors.AsType[*meshapi.StatusError](err)
	return ok && statusErr.Status >= 400 && statusErr.Status < 500
}
