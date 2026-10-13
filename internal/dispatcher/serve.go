package dispatcher

import (
	"context"
	"errors"
	"log"
	"os"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/http"
	"github.com/meshcloud/building-block-runner/internal/http/meshapi"
)

const claimRunYmlEnv = "RUNNER_CLAIM_RUN_YML"

// Serve claims runs until ctx ends, or runs only the run in the file that RUNNER_CLAIM_RUN_YML names.
func Serve(ctx context.Context, cfg config.Config, logger *log.Logger, userAgent http.UserAgent) error {
	if len(cfg.Dispatcher.ForTypes) == 0 && DefaultForTypes != nil {
		cfg.Dispatcher.ForTypes = DefaultForTypes()
	}
	if path := os.Getenv(claimRunYmlEnv); path != "" {
		if builtWithK8sTag {
			return errors.New(claimRunYmlEnv + " needs a binary built without -tags k8s")
		}
		return serveRunFile(ctx, cfg, logger, http.NewClient(userAgent), path)
	}
	return servePolling(ctx, cfg, logger, userAgent)
}

func servePolling(ctx context.Context, cfg config.Config, logger *log.Logger, userAgent http.UserAgent) error {
	if err := errors.Join(cfg.Validate(), cfg.ValidateDispatcher()); err != nil {
		return err
	}
	api, err := newRunnerClient(cfg, userAgent)
	if err != nil {
		return err
	}

	dispatcherCtx, stopDispatcher := context.WithCancel(ctx)
	defer stopDispatcher()
	dispatcher, err := Current(dispatcherCtx, cfg, api)
	if err != nil {
		return err
	}
	if cfg.Dispatcher.Register != nil {
		if err := register(ctx, cfg, logger, api, dispatcher); err != nil {
			return err
		}
	}
	logger.Printf("Claiming runs every %s", cfg.Dispatcher.PollingInterval)
	return newPoller(cfg, logger, api, dispatcher).poll(ctx)
}

func serveRunFile(ctx context.Context, cfg config.Config, logger *log.Logger, client http.Client, path string) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	run, err := ReadRunYaml(path)
	if err != nil {
		return err
	}
	api := meshapi.NewMeshRunnerClient(client.WithAuthorization(http.BearerToken(run.Spec.RunToken)), cfg.Api.Url.URL)
	// Whoever wrote the file owns the deadline, as a k8s job does with activeDeadlineSeconds.
	cfg.Dispatcher.Timeout.Duration = 0

	dispatcherCtx, stopDispatcher := context.WithCancel(ctx)
	defer stopDispatcher()
	dispatcher, err := Current(dispatcherCtx, cfg, api)
	if err != nil {
		return err
	}
	logger.Printf("Running the run in %s:\n%s", path, RedactedRunYaml(run))
	p := newPoller(cfg, logger, api, dispatcher)
	startErr := p.start(ctx, run)
	stopDispatcher()
	return errors.Join(startErr, p.drainStatus(ctx))
}

func newRunnerClient(cfg config.Config, userAgent http.UserAgent) (meshapi.MeshRunnerClient, error) {
	client := http.NewClient(userAgent)
	auth, err := newAuthorization(client, cfg.Api)
	if err != nil {
		return nil, err
	}
	return meshapi.NewMeshRunnerClient(client.WithAuthorization(auth), cfg.Api.Url.URL), nil
}
