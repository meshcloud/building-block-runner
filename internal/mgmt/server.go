// Package mgmt is the D12 unified observability listener: one process-level HTTP server
// exposing GET /healthz and GET /metrics on a single MANAGEMENT_PORT-resolved address
// (PLAN_HIGH_LEVEL.md D12, PLAN_DETAIL_04_single_binary.md §4.3), plus the generic
// runner_* Prometheus series every persona that lacks its own equivalent metrics wires
// in (RunMetrics -- every persona but run-controller, whose run_controller_* series
// already covers claim/dispatch, plan-04 §10.5). Not part of report (the run-status
// backchannel to the meshStack API -- a different concept, plan 03 §5.4) and not part of
// config (mgmt consumes config, it does not define it) -- a package of its own earns its
// place because it already has two consumers (the tf and controller personas) and four
// more arrive in phase 6 (P3).
package mgmt

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// healthzBody is byte-identical to the pre-D12 tf-block-runner healthz response (the
// former ad-hoc startHealthServer) so no deployed liveness probe observes a body change.
const healthzBody = "OK"

// Server is one process's management listener: /healthz and /metrics on the same
// address, never served twice on separate listeners (D12).
type Server struct {
	log  *slog.Logger
	addr string
	mux  *http.ServeMux
}

// NewServer builds a Server bound to addr (a net.Listen("tcp", ...) address, typically
// config.Port.Addr()) that exposes g via /metrics. g is a prometheus.Gatherer, not
// necessarily the process-default registry -- the run-controller persona passes
// prometheus.DefaultGatherer (its MetricsCollector still self-registers there, plan-04
// §1.1/§4.3), while personas with no existing default-registry metrics of their own use
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
// caller in this repo treats a returned error as fatal (P5, plan-04 §6): a
// liveness-probed listener that silently fails to bind defeats the point of D12 (the
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
// through promauto (internal/controller.NewMetricsCollector). Personas with no existing
// default-registry metrics of their own (i.e. tf) use this instead of reaching for the
// global registry directly, so their wiring code never needs to import
// prometheus/client_golang by name -- keeping the per-persona depguard trees minimal
// (plan-04 §4.5/§3.6).
func NewRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	reg.MustRegister(collectors.NewGoCollector())
	return reg
}
