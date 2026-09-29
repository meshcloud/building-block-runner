package tfrun

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
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

	_, err := backoff.Retry(ctx,
		func() (bool, error) {
			_, err := updateStateWithin(ctx, api, status, schedule.attemptTimeout)
			if err != nil && !isTransient(err) {
				return false, backoff.Permanent(err)
			}
			return true, err
		},
		backoff.WithBackOff(&backoff.ExponentialBackOff{
			InitialInterval:     schedule.initialDelay,
			MaxInterval:         schedule.maxDelay,
			Multiplier:          2,
			RandomizationFactor: backoff.DefaultRandomizationFactor,
		}),
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

func isTransient(err error) bool {
	if statusErr, ok := errors.AsType[*meshapi.StatusError](err); ok {
		return statusErr.Status >= 500
	}
	_, isTransportErr := errors.AsType[*url.Error](err)
	return isTransportErr
}
