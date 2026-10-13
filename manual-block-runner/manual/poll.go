package manual

import (
	"context"
	"errors"
	"log/slog"
	"time"
	"uuid"

	"github.com/meshcloud/building-block-runner/internal/http/meshapi"
)

const pollInterval = 10 * time.Second

type Poller struct {
	api        meshapi.MeshRunnerClient
	runnerUuid uuid.UUID
}

func NewPoller(api meshapi.MeshRunnerClient, runnerUuid uuid.UUID) Poller {
	return Poller{api: api, runnerUuid: runnerUuid}
}

func (p Poller) Run(ctx context.Context) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if p.executeNextRun(ctx) {
			continue
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
}

func (p Poller) executeNextRun(ctx context.Context) bool {
	// meshStack commits a claim before it answers, so a claimed run must be reported even during shutdown.
	ctx = context.WithoutCancel(ctx)
	run, err := p.api.ClaimRun(ctx, p.runnerUuid)
	if errors.Is(err, meshapi.ErrNoRunToClaim) {
		return false
	}
	if err != nil {
		slog.Error("cannot claim a run", "err", err)
		return false
	}
	if err := Execute(ctx, run, p.api.ForRun(run), p.runnerUuid); err != nil {
		slog.Error("cannot execute run", "err", err)
		return false
	}
	return true
}
