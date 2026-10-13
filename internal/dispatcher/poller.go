package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"
	"uuid"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/http/meshapi"
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
	logger     *log.Logger
	api        meshapi.MeshRunnerClient
	dispatcher Dispatcher
	// active holds nil for a run that this process did not start.
	active map[uuid.UUID]*Run
}

func newPoller(cfg config.Config, logger *log.Logger, api meshapi.MeshRunnerClient, dispatcher Dispatcher) *poller {
	return &poller{cfg: cfg, logger: logger, api: api, dispatcher: dispatcher, active: map[uuid.UUID]*Run{}}
}

func (p *poller) poll(ctx context.Context) error {
	ticker := time.NewTicker(p.cfg.Dispatcher.PollingInterval.Duration)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			p.logger.Printf("Stopped claiming, waiting for %d active runs", len(p.active))
			p.logErr(p.drainStatus(ctx))
			return nil
		case <-ticker.C:
			loopIterations.Inc()
			p.claimUpToCapacity(ctx)
		case status, open := <-p.dispatcher.Status():
			if !open {
				return errors.New("the dispatcher stopped before it was asked to")
			}
			p.logErr(p.track(ctx, status))
			if status.Ended {
				p.claimUpToCapacity(ctx)
			}
		}
	}
}

func (p *poller) drainStatus(ctx context.Context) error {
	var errs []error
	for status := range p.dispatcher.Status() {
		errs = append(errs, p.track(ctx, status))
	}
	return errors.Join(errs...)
}

func (p *poller) claimUpToCapacity(ctx context.Context) {
	capacity := p.capacity()
	if capacity == 0 {
		p.logger.Printf("At capacity with %d active runs, claiming no run", len(p.active))
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
	case errors.Is(err, meshapi.ErrNoRunToClaim):
		return false
	case err != nil:
		p.logger.Printf("Claiming a run failed: %v", err)
		return false
	}
	decrypted, err := p.cfg.Crypto.DecryptRun(run)
	if err != nil {
		decryptionErrors.WithLabelValues(p.cfg.Uuid.String()).Inc()
		p.logErr(p.reportFailure(ctx, run, "Failed to decrypt the run: "+err.Error()))
		return true
	}
	p.logErr(p.start(ctx, decrypted))
	return true
}

func (p *poller) claim(ctx context.Context) (Run, error) {
	defer observeDuration(claimDuration.WithLabelValues(p.cfg.Uuid.String()), time.Now())
	run, err := p.api.ClaimRun(ctx, p.cfg.Uuid)
	if err != nil && !errors.Is(err, meshapi.ErrNoRunToClaim) {
		claimErrors.WithLabelValues(p.cfg.Uuid.String(), errorTypeApi).Inc()
	}
	return run, err
}

func (p *poller) start(ctx context.Context, run Run) error {
	p.logger.Printf("Starting run %s", run.Metadata.Uuid)
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

func (p *poller) track(ctx context.Context, status RunStatus) error {
	run, known := p.active[status.RunUuid]
	if !status.Ended {
		if !known {
			p.active[status.RunUuid] = nil
		}
		return nil
	}
	delete(p.active, status.RunUuid)
	if status.Err == nil || errors.Is(status.Err, ErrFailureReported) {
		p.logger.Printf("Run %s ended: %v", status.RunUuid, status.Err)
		return nil
	}
	ctx = context.WithoutCancel(ctx)
	if run == nil {
		read, err := p.readRun(ctx, status.RunUuid)
		if err != nil {
			return fmt.Errorf("cannot report that run %s failed (%w): %w", status.RunUuid, status.Err, err)
		}
		run = &read
	}
	if err := p.reportFailure(ctx, *run, status.Err.Error()); err != nil {
		return err
	}
	if confirmer, ok := p.dispatcher.(FailureReportConfirmer); ok {
		confirmer.FailureReported(ctx, status.RunUuid)
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
	p.logger.Printf("Run %s failed: %s", run.Metadata.Uuid, message)
	client := p.api.ForRun(run)
	err := client.RegisterSource(ctx, meshapi.MeshRunnerRunSourceRegistration{
		Source: meshapi.MeshRunnerRunSource{Id: p.cfg.Uuid},
		Steps:  []meshapi.MeshRunnerRunStepRegistration{{Id: failureStepId, DisplayName: failureStepDisplayName}},
	})
	if err != nil {
		return fmt.Errorf("register as source of run %s: %w", run.Metadata.Uuid, err)
	}
	_, err = client.PatchStatus(ctx, p.cfg.Uuid, meshapi.MeshRunnerRunStatusUpdate{
		Status:  new(statusFailed),
		Summary: &message,
		Steps: []meshapi.MeshRunnerRunStepStatusUpdate{{
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

func (p *poller) logErr(err error) {
	if err != nil {
		p.logger.Print(err)
	}
}
