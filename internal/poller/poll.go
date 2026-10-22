package poller

import (
	"context"
	"log/slog"

	"github.com/meshcloud/meshstack-cli/pkg/setting"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatcher"
	"github.com/meshcloud/building-block-runner/internal/executor"
)

func Poll(ctx context.Context, cfg config.Config) error {
	if err := cfg.ValidateDispatcher(); err != nil {
		return err
	}
	sources := setting.Sources{setting.FallbackSource{Source: cfg.Meshstack}}
	meshStack, err := executor.ResolveClient(ctx, sources)
	if err != nil {
		return err
	}
	cfg.Meshstack.Endpoint = meshStack.Endpoint
	keycloakWif := newKeycloakWif(ctx, cfg.Dispatcher)

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
	if keycloakWif != nil {
		state.WorkloadIdentity = keycloakWif.Registration(state.KeycloakWifTokenPath)
	}
	p := newPoller(cfg, meshStack.BuildingBlockRun, runs, stopDispatcher, state.Running, keycloakWif)
	p.register = func(context.Context) error { return nil }
	if cfg.Dispatcher.Register != nil {
		p.register = func(ctx context.Context) error {
			return register(ctx, cfg, sources, meshStack.BuildingBlockRunner, state.WorkloadIdentity)
		}
	}
	return p.poll(ctx)
}

func newKeycloakWif(ctx context.Context, cfg config.Dispatcher) *dispatcher.KeycloakWif {
	if cfg.KeycloakWif == nil {
		return nil
	}
	if cfg.KeycloakWif.TokenLifetime.Duration < cfg.Timeout.Duration {
		slog.WarnContext(ctx, "dispatcher.keycloakWif.tokenLifetime is shorter than dispatcher.timeout, so a long run may use an expired token",
			"tokenLifetime", cfg.KeycloakWif.TokenLifetime, "timeout", cfg.Timeout)
	}
	return dispatcher.NewKeycloakWif(*cfg.KeycloakWif)
}
