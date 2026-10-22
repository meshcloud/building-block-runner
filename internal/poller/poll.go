package poller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/pkg/auth"
	"github.com/meshcloud/meshstack-cli/pkg/setting"

	"github.com/meshcloud/building-block-runner/internal/build"
	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatcher"
)

const (
	meshfedStartTimeout       = 10 * time.Minute
	meshfedStartRetryInterval = 10 * time.Second
)

func Poll(ctx context.Context, cfg config.Config) error {
	if err := errors.Join(cfg.Validate(), cfg.ValidateDispatcher()); err != nil {
		return err
	}
	settings := setting.SingleSource(cfg.Api)
	meshStack, err := newClientOnceMeshfedAnswers(ctx, settings, meshfedStartRetryInterval)
	if err != nil {
		return err
	}
	cfg.Api.Url = meshStack.Endpoint
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
	if cfg.Dispatcher.Register != nil {
		if err := register(ctx, cfg, settings, meshStack.BuildingBlockRunner, state.WorkloadIdentity); err != nil {
			return err
		}
	}
	slog.InfoContext(ctx, "claiming runs", "interval", cfg.Dispatcher.PollingInterval)
	return newPoller(cfg, meshStack.Runner, runs, stopDispatcher, state.Running, keycloakWif).poll(ctx)
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

// newClientOnceMeshfedAnswers retries, as meshfed-api may still be starting.
func newClientOnceMeshfedAnswers(ctx context.Context, settings setting.Sources, retryInterval time.Duration) (client.Client, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, meshfedStartTimeout, fmt.Errorf("gave up after %s", meshfedStartTimeout))
	defer cancel()
	for {
		meshStack, err := newClient(ctx, settings)
		if err == nil || !isUnanswered(err) {
			return meshStack, err
		}
		slog.WarnContext(ctx, "meshStack does not answer, retrying", "retryIn", retryInterval, "error", err)
		select {
		case <-ctx.Done():
			return client.Client{}, fmt.Errorf("build the meshStack client (%w): %w", context.Cause(ctx), err)
		case <-time.After(retryInterval):
		}
	}
}

func isUnanswered(err error) bool {
	if httpErr, ok := errors.AsType[client.HttpError](err); ok {
		return slices.Contains([]int{http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout}, httpErr.StatusCode)
	}
	_, isTransportErr := errors.AsType[*url.Error](err)
	return isTransportErr
}

func newClient(ctx context.Context, settings setting.Sources) (client.Client, error) {
	return auth.ResolveClient(ctx, clientOptions(settings))
}

func clientOptions(settings setting.Sources) auth.ResolveClientOptions {
	return auth.ResolveClientOptions{
		Version:        build.Version,
		GitHubRepo:     "meshcloud/building-block-runner",
		SettingSources: settings,
	}
}
