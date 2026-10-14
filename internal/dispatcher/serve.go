package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"slices"
	"time"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/pkg/auth"
	"github.com/meshcloud/meshstack-cli/pkg/setting"

	"github.com/meshcloud/building-block-runner/internal/build"
	"github.com/meshcloud/building-block-runner/internal/config"
)

const claimRunYmlEnv = "RUNNER_CLAIM_RUN_YML"

const (
	meshfedStartTimeout       = 10 * time.Minute
	meshfedStartRetryInterval = 10 * time.Second
)

// Serve claims runs until ctx ends, or runs only the run in the file that RUNNER_CLAIM_RUN_YML names.
func Serve(ctx context.Context, cfg config.Config, logger *log.Logger) error {
	if len(cfg.Dispatcher.ForTypes) == 0 && DefaultForTypes != nil {
		cfg.Dispatcher.ForTypes = DefaultForTypes()
	}
	if path := os.Getenv(claimRunYmlEnv); path != "" {
		if builtWithK8sTag {
			return errors.New(claimRunYmlEnv + " needs a binary built without -tags k8s")
		}
		return serveRunFile(ctx, cfg, logger, path)
	}
	return servePolling(ctx, cfg, logger)
}

func servePolling(ctx context.Context, cfg config.Config, logger *log.Logger) error {
	if err := errors.Join(cfg.Validate(), cfg.ValidateDispatcher()); err != nil {
		return err
	}
	settings := setting.SingleSource(cfg.Api)
	meshStack, err := newClientOnceMeshfedAnswers(ctx, logger, settings, meshfedStartRetryInterval)
	if err != nil {
		return err
	}
	// A k8s job gets the endpoint, which a profile or MESHSTACK_ENDPOINT may have supplied.
	cfg.Api.Url.URL = meshStack.Endpoint.URL

	dispatcherCtx, stopDispatcher := context.WithCancel(ctx)
	defer stopDispatcher()
	dispatcher, err := Current(dispatcherCtx, cfg, meshStack.Runner)
	if err != nil {
		return err
	}
	state, err := dispatcher.Init(ctx)
	if err != nil {
		return err
	}
	if cfg.Dispatcher.Register != nil {
		if err := register(ctx, cfg, logger, settings, meshStack.BuildingBlockRunner, state.WorkloadIdentity); err != nil {
			return err
		}
	}
	logger.Printf("Claiming runs every %s", cfg.Dispatcher.PollingInterval)
	return newPoller(cfg, logger, meshStack.Runner, dispatcher, state.Running).poll(ctx)
}

func serveRunFile(ctx context.Context, cfg config.Config, logger *log.Logger, path string) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	run, err := ReadRunYaml(path)
	if err != nil {
		return err
	}
	meshStack, err := newClient(ctx, runTokenSettings(cfg.Api, run, path))
	if err != nil {
		return err
	}
	// Whoever wrote the file owns the deadline, as a k8s job does with activeDeadlineSeconds.
	cfg.Dispatcher.Timeout.Duration = 0

	dispatcherCtx, stopDispatcher := context.WithCancel(ctx)
	defer stopDispatcher()
	dispatcher, err := Current(dispatcherCtx, cfg, meshStack.Runner)
	if err != nil {
		return err
	}
	if _, err := dispatcher.Init(ctx); err != nil {
		return err
	}
	logger.Printf("Running the run in %s:\n%s", path, RedactedRunYaml(run))
	p := newPoller(cfg, logger, meshStack.Runner, dispatcher, nil)
	startErr := p.start(ctx, run)
	stopDispatcher()
	return errors.Join(startErr, p.drainFinished(ctx))
}

// runTokenSettings leave out the configured auth, a second credential, and skip the version check the file's writer did.
func runTokenSettings(api config.Api, run Run, path string) setting.Sources {
	return setting.Sources{
		config.Api{Url: api.Url},
		setting.LookupSource(setting.ApiToken.EnvKey(), "the run token in "+path, func(context.Context) (string, error) {
			return run.Spec.RunToken, nil
		}),
		setting.LookupSource(setting.SkipVersionCheck.EnvKey(), "a run file", func(context.Context) (string, error) {
			return "true", nil
		}),
	}
}

// newClientOnceMeshfedAnswers retries, as meshfed-api may still be starting.
func newClientOnceMeshfedAnswers(ctx context.Context, logger *log.Logger, settings setting.Sources, retryInterval time.Duration) (client.Client, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, meshfedStartTimeout, fmt.Errorf("gave up after %s", meshfedStartTimeout))
	defer cancel()
	for {
		meshStack, err := newClient(ctx, settings)
		if err == nil || !isUnanswered(err) {
			return meshStack, err
		}
		logger.Printf("meshStack does not answer, retrying in %s: %v", retryInterval, err)
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
