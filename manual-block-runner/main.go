package main

import (
	"cmp"
	"context"
	"log/slog"
	"net"
	gohttp "net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"uuid"

	"github.com/meshcloud/building-block-runner/internal/dispatcher"
	"github.com/meshcloud/building-block-runner/internal/http"
	"github.com/meshcloud/building-block-runner/internal/http/meshapi"
	"github.com/meshcloud/building-block-runner/manual-block-runner/build"
	"github.com/meshcloud/building-block-runner/manual-block-runner/manual"
)

func main() {
	slog.Info("starting manual-block-runner", "version", build.Version)

	cfg, err := manual.LoadConfig(cmp.Or(os.Getenv("RUNNER_CONFIG_FILE"), "runner-config.yml"))
	if err != nil {
		slog.Error("cannot read config", "err", err)
		os.Exit(1)
	}
	runnerUuid, err := uuid.Parse(cfg.Uuid)
	if err != nil {
		slog.Error("invalid runner uuid", "err", err)
		os.Exit(1)
	}
	api, err := newRunnerClient(cfg)
	if err != nil {
		slog.Error("cannot create the meshStack client", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if runFile := os.Getenv("RUN_JSON_FILE_PATH"); runFile != "" {
		if err := executeRunFile(ctx, api, runnerUuid, runFile); err != nil {
			slog.Error("run failed", "err", err)
			os.Exit(1)
		}
		return
	}

	slog.Info("polling for runs", "runner", cfg.Uuid, "api", cfg.Api.Url)
	if err := startHealthServer(); err != nil {
		slog.Error("cannot start health server", "err", err)
		os.Exit(1)
	}
	manual.NewPoller(api, runnerUuid).Run(ctx)
}

func newRunnerClient(cfg manual.Config) (meshapi.MeshRunnerClient, error) {
	apiUrl, err := url.Parse(cfg.Api.Url)
	if err != nil {
		return nil, err
	}
	client := http.NewClient(http.UserAgent{GitHubRepo: "meshcloud/building-block-runner", Version: build.Version})
	var auth http.Authorization = http.BasicAuth{Username: cfg.Auth.Username, Password: cfg.Auth.Password}
	if cfg.Auth.ApiKey.ClientId != "" && cfg.Auth.ApiKey.ClientSecret != "" {
		auth = http.NewApiKeyLogin(client, apiUrl, cfg.Auth.ApiKey.ClientId, cfg.Auth.ApiKey.ClientSecret)
	}
	return meshapi.NewMeshRunnerClient(client.WithAuthorization(auth), apiUrl), nil
}

func executeRunFile(ctx context.Context, api meshapi.MeshRunnerClient, runnerUuid uuid.UUID, runFile string) error {
	run, err := dispatcher.ReadRunYaml(runFile)
	if err != nil {
		return err
	}
	return manual.Execute(ctx, run, api.ForRun(run), runnerUuid)
}

// startHealthServer listens on SERVER_PORT or PORT, which the Kotlin runner's Spring Boot read.
func startHealthServer() error {
	addr := ":" + cmp.Or(os.Getenv("SERVER_PORT"), os.Getenv("PORT"), "8104")
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	mux := gohttp.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w gohttp.ResponseWriter, _ *gohttp.Request) {
		_, _ = w.Write([]byte("OK"))
	})
	go func() {
		err := gohttp.Serve(listener, mux)
		slog.Error("health server stopped", "err", err)
		os.Exit(1)
	}()
	slog.Info("health server listening", "addr", addr)
	return nil
}
