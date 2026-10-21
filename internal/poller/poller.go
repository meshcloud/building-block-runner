package poller

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"uuid"

	"github.com/meshcloud/meshstack-cli/client"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatcher"
	"github.com/meshcloud/building-block-runner/internal/executor"
)

// maxClaimsPerCycleWhenUnlimited guards against an API that never runs out of runs.
const maxClaimsPerCycleWhenUnlimited = 10

type poller struct {
	cfg            config.Config
	api            client.MeshRunnerClient
	dispatcher     dispatcher.Dispatcher
	stopDispatcher context.CancelFunc
	active         map[uuid.UUID]activeRun
	unreported     int
}

// activeRun is zero for a run that an earlier process started.
type activeRun struct {
	run dispatcher.Run
}

func newPoller(cfg config.Config, api client.MeshRunnerClient, runs dispatcher.Dispatcher, stopDispatcher context.CancelFunc, running []uuid.UUID) *poller {
	active := map[uuid.UUID]activeRun{}
	for _, runUuid := range running {
		active[runUuid] = activeRun{}
	}
	return &poller{cfg: cfg, api: api, dispatcher: runs, stopDispatcher: stopDispatcher, active: active}
}

func (p *poller) poll(ctx context.Context) error {
	tick := time.Tick(p.cfg.Dispatcher.PollingInterval.Duration)
	p.claimUpToCapacity(ctx)
	for {
		select {
		case <-ctx.Done():
			return p.drain(ctx)
		case <-tick:
			p.claimUpToCapacity(ctx)
		case finished, open := <-p.dispatcher.Finished():
			if !open {
				return errors.New("the dispatcher stopped before it was asked to")
			}
			p.noteUnreported(ctx, p.track(ctx, finished))
			p.claimUpToCapacity(ctx)
		}
	}
}

func (p *poller) drain(ctx context.Context) error {
	slog.InfoContext(ctx, "waiting for active runs", "active", len(p.active))
	p.stopDispatcher()
	for finished := range p.dispatcher.Finished() {
		p.noteUnreported(ctx, p.track(ctx, finished))
	}
	if p.unreported > 0 {
		return fmt.Errorf("could not report %d failed runs", p.unreported)
	}
	return nil
}

func (p *poller) claimUpToCapacity(ctx context.Context) {
	capacity := p.capacity()
	if capacity == 0 {
		slog.InfoContext(ctx, "at capacity, claiming no run", "active", len(p.active))
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
	limit, limited := p.cfg.Dispatcher.MaxConcurrentRuns()
	if !limited {
		return maxClaimsPerCycleWhenUnlimited
	}
	return max(limit-len(p.active), 0)
}

func (p *poller) claimAndStart(ctx context.Context) (claimed bool) {
	run, err := p.api.ClaimRun(ctx, p.cfg.Uuid)
	switch {
	case errors.Is(err, client.ErrNoRunToClaim):
		return false
	case err != nil:
		slog.ErrorContext(ctx, "claiming a run failed", "error", err)
		return false
	}
	decrypted, err := p.cfg.Crypto.DecryptRun(run)
	if err != nil {
		p.noteUnreported(ctx, p.reportFailure(dispatcher.WithRun(ctx, run), run.Metadata.Uuid, "Failed to decrypt the run: "+err.Error()))
		return true
	}
	p.noteUnreported(ctx, p.start(ctx, decrypted))
	return true
}

func (p *poller) start(ctx context.Context, run dispatcher.Run) error {
	ctx = dispatcher.WithRun(ctx, run)
	runnerType, _ := executor.RunnerTypeOf(run)
	slog.InfoContext(ctx, "starting run", "behavior", run.Spec.Behavior, "runnerType", runnerType)
	if err := p.dispatcher.Dispatch(ctx, run, ""); err != nil {
		return p.reportFailure(ctx, run.Metadata.Uuid, "Failed to start the run: "+err.Error())
	}
	p.active[run.Metadata.Uuid] = activeRun{run: run}
	return nil
}

func (p *poller) track(ctx context.Context, finished dispatcher.Finished) error {
	active := p.active[finished.RunUuid]
	delete(p.active, finished.RunUuid)
	run := active.run
	run.Metadata.Uuid = finished.RunUuid
	ctx = dispatcher.WithRun(ctx, run)
	switch finished.ExitCode {
	case executor.ExitStatusDelivered:
		slog.InfoContext(ctx, "run finished")
		return nil
	case executor.ExitFailedDelivered:
		slog.InfoContext(ctx, "run finished with its failure reported")
		return nil
	}
	ctx = context.WithoutCancel(ctx)
	message := cmp.Or(finished.Failure, fmt.Sprintf("The run ended with exit code %d before it reported its status.", finished.ExitCode))
	if err := p.reportFailure(ctx, finished.RunUuid, message); err != nil {
		return err
	}
	if finished.Reported != nil {
		finished.Reported(ctx)
	}
	return nil
}

// reportFailure registers this runner as a source, as no other source may exist for the run.
func (p *poller) reportFailure(ctx context.Context, runUuid uuid.UUID, message string) error {
	slog.WarnContext(ctx, "run failed", "message", message)
	if err := executor.ReportFailure(ctx, p.api.ForRunUuid(runUuid), p.cfg.Uuid, message); err != nil {
		return fmt.Errorf("run %s: %w", runUuid, err)
	}
	return nil
}

func (p *poller) noteUnreported(ctx context.Context, err error) {
	if err != nil {
		p.unreported++
		slog.ErrorContext(ctx, "could not report a failed run", "error", err)
	}
}
