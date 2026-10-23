package metrics

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/meshcloud/building-block-runner/internal/config"
)

const shutdownTimeout = 10 * time.Second

type Server struct {
	server   *http.Server
	listener net.Listener
}

// Listen opens metrics.port, so that a port in use fails before the runner claims a run.
func Listen(ctx context.Context, cfg config.Config) (*Server, error) {
	listener, err := new(net.ListenConfig).Listen(ctx, "tcp", ":"+strconv.Itoa(cfg.Metrics.Port))
	if err != nil {
		return nil, err
	}
	setFromConfig(cfg)
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{}))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("OK"))
	})
	slog.InfoContext(ctx, "serving metrics", "port", cfg.Metrics.Port)
	return &Server{
		server:   &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second},
		listener: listener,
	}, nil
}

// Serve serves /metrics and /healthz until ctx ends.
func (s *Server) Serve(ctx context.Context) error {
	served := make(chan error, 1)
	go func() { served <- s.server.Serve(s.listener) }()
	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := s.server.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
