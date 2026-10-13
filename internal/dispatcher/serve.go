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
	runYml := os.Getenv(claimRunYmlEnv)
	if err := validate(cfg, runYml); err != nil {
		return err
	}
	api, err := newRunnerClient(cfg, userAgent)
	if err != nil {
		return err
	}

	dispatcherCtx, stopDispatcher := context.WithCancel(ctx)
	defer stopDispatcher()
	dispatcher, err := Current(dispatcherCtx, cfg)
	if err != nil {
		return err
	}
	p := newPoller(cfg, logger, api, dispatcher)
	if runYml != "" {
		return p.runFile(ctx, runYml, stopDispatcher)
	}

	if cfg.Dispatcher.Register != nil {
		if err := register(ctx, cfg, logger, api, dispatcher); err != nil {
			return err
		}
	}
	logger.Printf("Claiming runs every %s", cfg.Dispatcher.PollingInterval)
	return p.poll(ctx)
}

func validate(cfg config.Config, runYml string) error {
	if runYml != "" {
		return cfg.Validate()
	}
	return errors.Join(cfg.Validate(), cfg.ValidateDispatcher())
}

func newRunnerClient(cfg config.Config, userAgent http.UserAgent) (meshapi.MeshRunnerClient, error) {
	client := http.NewClient(userAgent)
	auth, err := newAuthorization(client, cfg.Api)
	if err != nil {
		return nil, err
	}
	return meshapi.NewMeshRunnerClient(client.WithAuthorization(auth), cfg.Api.Url.URL), nil
}

func (p *poller) runFile(ctx context.Context, path string, stopDispatcher context.CancelFunc) error {
	run, err := ReadRunYaml(path)
	if err != nil {
		return err
	}
	p.logger.Printf("Running the run in %s:\n%s", path, RedactedRunYaml(run))
	startErr := p.start(ctx, run)
	stopDispatcher()
	return errors.Join(startErr, p.drainStatus(ctx))
}
