package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"uuid"

	"github.com/meshcloud/meshstack-cli/client"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/logctx"
)

// maxClaimsPerCycleWhenUnlimited guards against an API that never runs out of runs.
const maxClaimsPerCycleWhenUnlimited = 10

const (
	failureStepId          = "validation"
	failureStepDisplayName = "Validation"
	statusFailed           = "FAILED"
)

type poller struct {
	cfg        config.Config
	api        client.MeshRunnerClient
	dispatcher Dispatcher
	// active holds nil for a run that this process did not start.
	active map[uuid.UUID]*Run
}

func newPoller(cfg config.Config, api client.MeshRunnerClient, dispatcher Dispatcher, running []uuid.UUID) *poller {
	active := map[uuid.UUID]*Run{}
	for _, runUuid := range running {
		active[runUuid] = nil
	}
	return &poller{cfg: cfg, api: api, dispatcher: dispatcher, active: active}
}

func (p *poller) poll(ctx context.Context) error {
	ticker := time.NewTicker(p.cfg.Dispatcher.PollingInterval.Duration)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.InfoContext(ctx, "stopped claiming, waiting for active runs", "active", len(p.active))
			p.logErr(ctx, p.drainFinished(ctx))
			return nil
		case <-ticker.C:
			loopIterations.Inc()
			p.claimUpToCapacity(ctx)
		case finished, open := <-p.dispatcher.Finished():
			if !open {
				return errors.New("the dispatcher stopped before it was asked to")
			}
			p.logErr(ctx, p.track(ctx, finished))
			p.claimUpToCapacity(ctx)
		}
	}
}

func (p *poller) drainFinished(ctx context.Context) error {
	var errs []error
	for finished := range p.dispatcher.Finished() {
		errs = append(errs, p.track(ctx, finished))
	}
	return errors.Join(errs...)
}

func (p *poller) claimUpToCapacity(ctx context.Context) {
	capacity := p.capacity()
	if capacity == 0 {
		slog.InfoContext(ctx, "at capacity, claiming no run", "active", len(p.active))
		atCapacitySkips.WithLabelValues(p.cfg.Uuid.String()).Inc()
		return
	}
	for range capacity {
		// Shutdown must not cut off a claim or a failure report, or the run stays claimed without a status.
		if ctx.Err() != nil || !p.claimAndStart(context.WithoutCancel(ctx)) {
			return
		}
	}
}

// capacity keeps the poller from claiming a run that it would have to report FAILED.
func (p *poller) capacity() int {
	if p.cfg.Dispatcher.MaxConcurrentJobs < 0 {
		return maxClaimsPerCycleWhenUnlimited
	}
	return max(p.cfg.Dispatcher.MaxConcurrentJobs-len(p.active), 0)
}

func (p *poller) claimAndStart(ctx context.Context) (claimed bool) {
	run, err := p.claim(ctx)
	switch {
	case errors.Is(err, client.ErrNoRunToClaim):
		return false
	case err != nil:
		slog.ErrorContext(ctx, "claiming a run failed", "error", err)
		return false
	}
	decrypted, err := p.cfg.Crypto.DecryptRun(run)
	if err != nil {
		decryptionErrors.WithLabelValues(p.cfg.Uuid.String()).Inc()
		p.logErr(ctx, p.reportFailure(ctx, run, "Failed to decrypt the run: "+err.Error()))
		return true
	}
	p.logErr(ctx, p.start(ctx, decrypted))
	return true
}

func (p *poller) claim(ctx context.Context) (Run, error) {
	defer observeDuration(claimDuration.WithLabelValues(p.cfg.Uuid.String()), time.Now())
	run, err := p.api.ClaimRun(ctx, p.cfg.Uuid)
	if err != nil && !errors.Is(err, client.ErrNoRunToClaim) {
		claimErrors.WithLabelValues(p.cfg.Uuid.String(), errorTypeApi).Inc()
	}
	return run, err
}

func (p *poller) start(ctx context.Context, run Run) error {
	ctx = withRun(ctx, run)
	slog.InfoContext(ctx, "starting run")
	started := time.Now()
	err := p.dispatcher.Start(ctx, run)
	observeDuration(startDuration.WithLabelValues(p.cfg.Uuid.String()), started)
	if err != nil {
		startErrors.WithLabelValues(p.cfg.Uuid.String(), startErrorType(err)).Inc()
		return p.reportFailure(ctx, run, "Failed to start the run: "+err.Error())
	}
	runsStarted.WithLabelValues(p.cfg.Uuid.String()).Inc()
	p.active[run.Metadata.Uuid] = &run
	return nil
}

func startErrorType(err error) string {
	if errors.Is(err, ErrRunTooLarge) {
		return errorTypeRunTooLarge
	}
	return errorTypeStart
}

func (p *poller) track(ctx context.Context, finished RunFinished) error {
	run := p.active[finished.RunUuid]
	delete(p.active, finished.RunUuid)
	if run != nil {
		ctx = withRun(ctx, *run)
	} else {
		ctx = logctx.With(ctx, slog.Any("run", finished.RunUuid))
	}
	switch {
	case finished.Err == nil:
		slog.InfoContext(ctx, "run finished")
		return nil
	case errors.Is(finished.Err, ErrFailureReported):
		slog.InfoContext(ctx, "run finished with its failure reported", "error", finished.Err)
		return nil
	}
	ctx = context.WithoutCancel(ctx)
	if run == nil {
		read, err := p.readRun(ctx, finished.RunUuid)
		if err != nil {
			return fmt.Errorf("cannot report that run %s failed (%w): %w", finished.RunUuid, finished.Err, err)
		}
		run = &read
	}
	if err := p.reportFailure(ctx, *run, finished.Err.Error()); err != nil {
		return err
	}
	if confirmer, ok := p.dispatcher.(FailureReportConfirmer); ok {
		confirmer.FailureReported(ctx, finished.RunUuid)
	}
	return nil
}

func (p *poller) readRun(ctx context.Context, runUuid uuid.UUID) (Run, error) {
	reader, ok := p.dispatcher.(RunReader)
	if !ok {
		return Run{}, errors.New("this process did not start it")
	}
	return reader.ReadRun(ctx, runUuid)
}

// reportFailure registers this runner as a source, as no other source may exist for the run.
func (p *poller) reportFailure(ctx context.Context, run Run, message string) error {
	ctx = withRun(ctx, run)
	slog.WarnContext(ctx, "run failed", "message", message)
	runClient := p.api.ForRun(run)
	err := runClient.RegisterSource(ctx, client.MeshRunnerRunSourceRegistration{
		Source: client.MeshRunnerRunSource{Id: p.cfg.Uuid},
		Steps:  []client.MeshRunnerRunStepRegistration{{Id: failureStepId, DisplayName: failureStepDisplayName}},
	})
	if err != nil {
		return fmt.Errorf("register as source of run %s: %w", run.Metadata.Uuid, err)
	}
	_, err = runClient.PatchStatus(ctx, p.cfg.Uuid, client.MeshRunnerRunStatusUpdate{
		Status:  new(statusFailed),
		Summary: &message,
		Steps: []client.MeshRunnerRunStepStatusUpdate{{
			Id:            failureStepId,
			DisplayName:   failureStepDisplayName,
			Status:        new(statusFailed),
			UserMessage:   &message,
			SystemMessage: &message,
		}},
	})
	if err != nil {
		return fmt.Errorf("report that run %s failed: %w", run.Metadata.Uuid, err)
	}
	return nil
}

func (*poller) logErr(ctx context.Context, err error) {
	if err != nil {
		slog.ErrorContext(ctx, "could not report a failed run", "error", err)
	}
}
