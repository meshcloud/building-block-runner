package tfrun

import (
	"context"
	"errors"
	"log"
	"net/url"
	"time"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
)

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
	attemptTimeout: 30 * time.Second,
	giveUpAfter:    10 * time.Minute,
}

// updateStateWithRetry sends a status update that no later update repeats. If it is lost, meshfed shows
// the run as in progress until its stuck-run timeout fails it, hours later.
func updateStateWithRetry(api RunApi, status *RunStatus, schedule retrySchedule, logger *log.Logger) error {
	deadline := time.Now().Add(schedule.giveUpAfter)
	delay := schedule.initialDelay
	for {
		err := updateStateWithin(api, status, min(schedule.attemptTimeout, time.Until(deadline)))
		if err == nil || !isTransient(err) || time.Now().Add(delay).After(deadline) {
			return err
		}
		logger.Printf("Status update for run %s failed, retrying in %s: %v", status.RunId, delay, err)
		time.Sleep(delay)
		delay = min(delay*2, schedule.maxDelay)
	}
}

func updateStateWithin(api RunApi, status *RunStatus, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, err := api.UpdateState(ctx, status)
	return err
}

func isTransient(err error) bool {
	var statusErr *meshapi.StatusError
	if errors.As(err, &statusErr) {
		return statusErr.Status >= 500
	}
	var transportErr *url.Error
	return errors.As(err, &transportErr)
}
