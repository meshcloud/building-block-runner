package executor

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/meshcloud/meshstack-cli/client"
)

const (
	statusUpdateInterval = 10 * time.Second
	// keepaliveInterval sends an unchanged status now and then, as only its answer tells of an abort.
	keepaliveInterval = time.Minute
)

var errAborted = errors.New("meshStack aborted the run")

type Reporter struct {
	api      client.MeshBuildingBlockRunClient
	runUuid  string
	sourceId string
	logCtx   context.Context //nolint:containedctx // a step log is an io.Writer, so it takes the context for its debug lines from here.

	mu              sync.Mutex
	registered      bool
	status          Status
	failure         string
	steps           []*Step
	changesDetected *bool
	handOver        bool
	abortRequested  bool

	lastSent   client.MeshBuildingBlockRunStatusUpdate
	lastSentAt time.Time
}

func NewReporter(api client.MeshBuildingBlockRunClient, runUuid, sourceId string) *Reporter {
	return &Reporter{api: api, runUuid: runUuid, sourceId: sourceId, status: StatusInProgress, logCtx: context.Background()}
}

func (r *Reporter) Step(id, displayName string) *Step {
	r.mu.Lock()
	defer r.mu.Unlock()
	step := &Step{r: r, id: id, displayName: displayName, status: StatusPending}
	r.steps = append(r.steps, step)
	return step
}

func (r *Reporter) Register(ctx context.Context) error {
	r.mu.Lock()
	registrations := make([]client.MeshBuildingBlockRunStepRegistration, 0, len(r.steps))
	for _, step := range r.steps {
		registrations = append(registrations, client.MeshBuildingBlockRunStepRegistration{Id: step.id, DisplayName: step.displayName})
	}
	r.mu.Unlock()
	err := r.api.RegisterSource(ctx, r.runUuid, client.MeshBuildingBlockRunSourceRegistration{
		Source: client.MeshBuildingBlockRunSource{Id: r.sourceId},
		Steps:  registrations,
	})
	if err != nil {
		return fmt.Errorf("register as source of the run: %w", err)
	}
	slog.InfoContext(ctx, "registered as source of the run", "source", r.sourceId)

	r.mu.Lock()
	defer r.mu.Unlock()
	r.registered = true
	r.logCtx = context.WithoutCancel(ctx)
	return nil
}

func (r *Reporter) SetChangesDetected(changed bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.changesDetected = &changed
}

func (r *Reporter) HandOver() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handOver = true
}

func (r *Reporter) Finish(ctx context.Context) (Status, error) {
	r.mu.Lock()
	for _, step := range r.steps {
		step.flushPartialLine(ctx)
	}
	if r.status != StatusFailed && !r.allSucceeded() {
		r.fail("The run ended without a final status.")
	}
	if !r.registered {
		failure := cmp.Or(r.failure, "The run ended before it registered.")
		r.mu.Unlock()
		return StatusFailed, ReportFailure(ctx, r.api, r.runUuid, r.sourceId, failure)
	}
	status := r.finalStatus()
	update := r.update(status)
	r.mu.Unlock()

	slog.InfoContext(ctx, "sending the final status", "status", status)
	_, err := r.api.UpdateSourceSteps(ctx, r.runUuid, r.sourceId, update)
	httpErr, isHttpErr := errors.AsType[client.HttpError](err)
	switch {
	case err == nil:
		return status, nil
	case status == StatusAborted && isHttpErr && httpErr.IsClientError():
		slog.InfoContext(ctx, "meshStack refused ABORTED, as it already ended the aborted run", "error", err)
		return status, nil
	}
	return status, fmt.Errorf("send the final status %s: %w", status, err)
}

func (r *Reporter) allSucceeded() bool {
	return len(r.steps) > 0 && !slices.ContainsFunc(r.steps, func(step *Step) bool { return step.status != StatusSucceeded })
}

// fail fails the first step that did not succeed, or none when the run declared none.
func (r *Reporter) fail(userMessage string) {
	r.status = StatusFailed
	r.failure = userMessage
	if first := slices.IndexFunc(r.steps, func(step *Step) bool { return step.status != StatusSucceeded }); first >= 0 {
		r.steps[first].failLocked(userMessage)
	}
}

func (r *Reporter) failUnlessFinal(userMessage string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status != StatusFailed {
		r.fail(userMessage)
	}
}

func (r *Reporter) observe(ctx context.Context, cancelRun context.CancelCauseFunc) (stop func()) {
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		tick := time.Tick(statusUpdateInterval)
		for {
			select {
			case <-done:
				return
			case <-tick:
				r.sendProgress(ctx, cancelRun)
			}
		}
	})
	return func() {
		close(done)
		wg.Wait()
	}
}

func (r *Reporter) sendProgress(ctx context.Context, cancelRun context.CancelCauseFunc) {
	update, ok := r.progressUpdate()
	if !ok {
		return
	}
	result, err := r.api.UpdateSourceSteps(ctx, r.runUuid, r.sourceId, update)
	if err != nil {
		slog.WarnContext(ctx, "status update failed", "error", err)
		return
	}
	r.markSent(update)
	if result.Abort && r.requestAbort() {
		slog.InfoContext(ctx, "meshStack aborted the run, cancelling it")
		cancelRun(errAborted)
	}
}

func (r *Reporter) progressUpdate() (client.MeshBuildingBlockRunStatusUpdate, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.registered || r.status == StatusFailed {
		return client.MeshBuildingBlockRunStatusUpdate{}, false
	}
	update := r.update(r.status)
	if reflect.DeepEqual(update, r.lastSent) && time.Since(r.lastSentAt) < keepaliveInterval {
		return client.MeshBuildingBlockRunStatusUpdate{}, false
	}
	return update, true
}

func (r *Reporter) markSent(update client.MeshBuildingBlockRunStatusUpdate) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastSent, r.lastSentAt = update, time.Now()
}

func (r *Reporter) requestAbort() (first bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	first = !r.abortRequested
	r.abortRequested = true
	return
}

func (r *Reporter) finalStatus() Status {
	switch {
	case r.abortRequested:
		return StatusAborted
	case r.status == StatusFailed:
		return StatusFailed
	case r.handOver:
		return StatusInProgress
	}
	return StatusSucceeded
}

func (r *Reporter) update(status Status) client.MeshBuildingBlockRunStatusUpdate {
	var steps []client.MeshBuildingBlockRunStepStatusUpdate
	for _, step := range r.steps {
		steps = append(steps, step.update())
	}
	return client.MeshBuildingBlockRunStatusUpdate{
		Status:          new(string(status)),
		Steps:           steps,
		ChangesDetected: r.changesDetected,
	}
}
