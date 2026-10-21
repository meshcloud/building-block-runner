package poller

import (
	"context"
	"log/slog"

	"github.com/meshcloud/meshstack-cli/pkg/setting"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatcher"
)

func Poll(ctx context.Context, cfg config.Config) error {
	if err := cfg.ValidateDispatcher(); err != nil {
		return err
	}
	settings := setting.SingleSource(cfg.Api)
	meshStack, err := newClientOnceMeshfedAnswers(ctx, settings, meshfedStartRetryInterval)
	if err != nil {
		return err
	}
	cfg.Api.Url = meshStack.Endpoint

	// Only drain stops the dispatcher, as its closed Finished would otherwise race the poller's ctx.Done.
	dispatcherCtx, stopDispatcher := context.WithCancel(context.WithoutCancel(ctx))
	defer stopDispatcher()
	runs, err := dispatcher.Current(dispatcherCtx, cfg)
	if err != nil {
		return err
	}
	state, err := runs.Init(ctx)
	if err != nil {
		return err
	}
	if cfg.Dispatcher.Register != nil {
		if err := register(ctx, cfg, settings, meshStack.BuildingBlockRunner, state.WorkloadIdentity); err != nil {
			return err
		}
	}
	slog.InfoContext(ctx, "claiming runs", "interval", cfg.Dispatcher.PollingInterval)
	return newPoller(cfg, meshStack.Runner, runs, stopDispatcher, state.Running).poll(ctx)
}
