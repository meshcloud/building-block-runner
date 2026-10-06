package manual

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
)

const pollInterval = 10 * time.Second

type Poller struct {
	cfg    Config
	client *meshapi.Client
}

func NewPoller(cfg Config) Poller {
	var auth meshapi.AuthProvider = meshapi.BasicAuth{Username: cfg.Auth.Username, Password: cfg.Auth.Password}
	if cfg.Auth.ApiKey.ClientId != "" && cfg.Auth.ApiKey.ClientSecret != "" {
		auth = meshapi.NewApiKeyAuth(cfg.Api.Url, cfg.Auth.ApiKey.ClientId, cfg.Auth.ApiKey.ClientSecret)
	}
	return Poller{cfg: cfg, client: meshapi.NewClientWithHTTP(cfg.Api.Url, cfg.Uuid, auth, meshStackHTTP)}
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
	_, data, err := p.client.FetchRun(p.cfg.Uuid)
	if statusErr, ok := errors.AsType[*meshapi.StatusError](err); ok && statusErr.Status == http.StatusNotFound {
		return false
	}
	if err != nil {
		slog.Error("cannot fetch a run", "err", err)
		return false
	}

	run, err := parseRun(data)
	if err != nil {
		slog.Error("cannot parse the fetched run", "err", err)
		return false
	}
	// meshStack has claimed the run, so it must be reported even during shutdown.
	if err := ExecuteRun(context.WithoutCancel(ctx), p.cfg, run); err != nil {
		slog.Error("cannot execute run", "err", err)
		return false
	}
	return true
}
