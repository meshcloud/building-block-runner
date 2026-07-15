// Package observability is the unified observability listener: one process-level HTTP server
// exposing GET /healthz and GET /metrics on a single MANAGEMENT_PORT-resolved address,
// plus the generic
// runner_* Prometheus series every type that lacks its own equivalent metrics wires
// in (RunMetrics -- every type but run-controller, whose run_controller_* series
// already covers claim/dispatch). Not part of report (the run-status
// backchannel to the meshStack API -- a different concept) and not part of
// config (observability consumes config, it does not define it) -- a package of its own earns its
// place because it already has two consumers (the tf and controller runner types) and four
// more arrive later.
package observability

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// healthzBody is byte-identical to the earlier tf-block-runner healthz response (the
// former ad-hoc startHealthServer) so no deployed liveness probe observes a body change.
const healthzBody = "OK"

// Server is one process's management listener: /healthz and /metrics on the same
// address, never served twice on separate listeners.
type Server struct {
	log  *slog.Logger
	addr string
	mux  *http.ServeMux
}

// NewServer builds a Server bound to addr (a net.Listen("tcp", ...) address, typically
// config.Port.Addr()) that exposes g via /metrics. g is a prometheus.Gatherer, not
// necessarily the process-default registry -- the run-controller type passes
// prometheus.DefaultGatherer (its MetricsCollector still self-registers there), while
// runner types with no existing default-registry metrics of their own use
// NewRegistry instead.
func NewServer(log *slog.Logger, addr string, g prometheus.Gatherer) Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(healthzBody))
	})
	mux.Handle("/metrics", promhttp.HandlerFor(g, promhttp.HandlerOpts{}))
	return Server{log: log, addr: addr, mux: mux}
}

// Handler exposes the underlying mux for tests (httptest.NewServer / ServeHTTP) without
// going through a real TCP bind.
func (s Server) Handler() http.Handler {
	return s.mux
}

// Start binds the listener synchronously -- so a bind failure is visible to the caller
// before startup proceeds any further -- then serves in a background goroutine. Every
// caller in this repo treats a returned error as fatal: a
// liveness-probed listener that silently fails to bind defeats the point of the unified listener (the
// sanctioned behavior change for run-controller, which used to log-and-continue).
func (s Server) Start() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("management listener failed to bind on %s: %w", s.addr, err)
	}
	s.log.Info("management listener started", "addr", s.addr)
	go func() {
		if err := http.Serve(ln, s.mux); err != nil {
			s.log.Error("management listener stopped", "error", err)
		}
	}()
	return nil
}

// NewRegistry returns a fresh, process-local registry pre-populated with the standard
// Go/process collectors -- the baseline every /metrics exposition should carry,
// matching what the process-default registry already includes for metrics registered
// through promauto (internal/controller.NewMetricsCollector). Runner types with no existing
// default-registry metrics of their own (i.e. tf) use this instead of reaching for the
// global registry directly, so their wiring code never needs to import
// prometheus/client_golang by name -- keeping the per-type depguard trees minimal.
func NewRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	reg.MustRegister(collectors.NewGoCollector())
	return reg
}
