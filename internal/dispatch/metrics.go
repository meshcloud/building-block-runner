package dispatch

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	metricsInstance *MetricsCollector
	metricsOnce     sync.Once
)

// Error type constants for metrics labels. All error types used in metrics are defined
// here for discoverability -- moved verbatim from the former internal/controller/metrics.go
// (names/labels are the frozen run_controller_* surface).
const (
	// Run fetch error types.
	ErrorTypeFetchAPI = "api_error"

	// Job creation error types.
	ErrorTypeJobCreation = "job_creation_error"
	ErrorTypeRunTooLarge = "run_too_large"

	// Service account error types.
	ErrorTypeServiceAccountCreation = "creation_error"

	// Runner registration error types.
	ErrorTypeRegistrationMarshal = "marshal_error"
	ErrorTypeRegistrationPut     = "put_error"
)

// MetricsCollector holds all Prometheus metrics for the run-controller type. It is the
// dissolution target of the former internal/controller/metrics.go: the
// metric names/labels are a frozen operator-facing surface and are unchanged by the
// move. internal/k8sjob never imports prometheus directly (depguard) -- it drives these
// same series through the small consumer-side JobMetrics interface it declares, satisfied
// structurally by the exported methods below.
type MetricsCollector struct {
	// Run fetch metrics
	runsFetchErrors   *prometheus.CounterVec
	runsFetchDuration *prometheus.HistogramVec

	// Job creation metrics
	jobsCreatedTotal    *prometheus.CounterVec
	jobCreationErrors   *prometheus.CounterVec
	jobCreationDuration *prometheus.HistogramVec

	// Capacity metrics
	jobsAtCapacitySkips *prometheus.CounterVec

	// Service account metrics
	serviceAccountsCreatedTotal  *prometheus.CounterVec
	serviceAccountCreationErrors *prometheus.CounterVec

	// Decryption metrics
	decryptionErrors *prometheus.CounterVec

	// Runner registration metrics
	runnerRegistrationSuccess *prometheus.CounterVec
	runnerRegistrationErrors  *prometheus.CounterVec

	// Controller health metrics
	controllerLoopIterations prometheus.Counter
	activeRunners            prometheus.Gauge
}

// NewMetricsCollector creates and registers all Prometheus metrics against the process
// default registry (prometheus.DefaultRegisterer). Uses a singleton so the three historic
// call sites (controller, claim client, registration path) share one instance and repeated
// construction never trips a duplicate-registration panic -- the customer-facing behavior
// preserved for every type wiring that does not pass its own registry.
//
// It is a thin default-registry wrapper over NewMetricsCollectorWithRegistry (the
// injectable seam): production wiring that wants the run_controller_* series
// on a dedicated, process-local registry (e.g. the tf type's observability.NewRegistry) or a test
// that wants an isolated registry calls NewMetricsCollectorWithRegistry directly.
func NewMetricsCollector() *MetricsCollector {
	metricsOnce.Do(func() {
		metricsInstance = NewMetricsCollectorWithRegistry(prometheus.DefaultRegisterer)
	})
	return metricsInstance
}

// NewMetricsCollectorWithRegistry is the injectable metrics seam: it builds and
// registers the run_controller_* series against reg -- any
// prometheus.Registerer, not necessarily the process default (see observability.NewRegistry for the
// per-type dedicated registry, and prometheus.NewRegistry for the isolated per-test one
// that removes the reliance on NewMetricsCollector's sync.Once). Metric names, labels and
// help strings are byte-identical to the singleton (scrape-visible surface); only the
// registry the series land on is injectable. Unlike NewMetricsCollector this returns a fresh
// collector every call, so two independent registries never share counters.
func NewMetricsCollectorWithRegistry(reg prometheus.Registerer) *MetricsCollector {
	factory := promauto.With(reg)
	return &MetricsCollector{
		runsFetchErrors: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "run_controller_runs_fetch_errors_total",
				Help: "Total number of errors while fetching building block runs",
			},
			[]string{"controller_uuid", "error_type"},
		),
		runsFetchDuration: factory.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "run_controller_runs_fetch_duration_seconds",
				Help:    "Duration of run fetch operations in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"controller_uuid"},
		),
		jobsCreatedTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "run_controller_jobs_created_total",
				Help: "Total number of Kubernetes jobs created for building block runs",
			},
			[]string{"controller_uuid"},
		),
		jobCreationErrors: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "run_controller_job_creation_errors_total",
				Help: "Total number of errors while creating Kubernetes jobs",
			},
			[]string{"controller_uuid", "error_type"},
		),
		jobCreationDuration: factory.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "run_controller_job_creation_duration_seconds",
				Help:    "Duration of Kubernetes job creation operations in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"controller_uuid"},
		),
		jobsAtCapacitySkips: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "run_controller_jobs_at_capacity_skips_total",
				Help: "Total number of polling cycles skipped because the controller was at its max concurrent jobs limit",
			},
			[]string{"controller_uuid"},
		),
		serviceAccountsCreatedTotal: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "run_controller_service_accounts_created_total",
				Help: "Total number of Kubernetes service accounts created for workload identity",
			},
			[]string{"controller_uuid"},
		),
		serviceAccountCreationErrors: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "run_controller_service_account_creation_errors_total",
				Help: "Total number of errors while creating Kubernetes service accounts",
			},
			[]string{"controller_uuid", "error_type"},
		),
		decryptionErrors: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "run_controller_decryption_errors_total",
				Help: "Total number of errors while decrypting run details",
			},
			[]string{"controller_uuid"},
		),
		runnerRegistrationSuccess: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "run_controller_runner_registration_success_total",
				Help: "Total number of successful runner registrations on startup",
			},
			[]string{"controller_uuid"},
		),
		runnerRegistrationErrors: factory.NewCounterVec(
			prometheus.CounterOpts{
				Name: "run_controller_runner_registration_errors_total",
				Help: "Total number of runner registration errors on startup",
			},
			[]string{"controller_uuid", "error_type"},
		),
		controllerLoopIterations: factory.NewCounter(
			prometheus.CounterOpts{
				Name: "run_controller_loop_iterations_total",
				Help: "Total number of controller polling loop iterations",
			},
		),
		activeRunners: factory.NewGauge(
			prometheus.GaugeOpts{
				Name: "run_controller_active_runners",
				Help: "Number of active runners configured in the controller",
			},
		),
	}
}

// The methods below are the exported surface internal/k8sjob and the cmd/bbrunner wiring
// drive these metrics through (k8sjob declares its own small JobMetrics interface,
// structurally satisfied by *MetricsCollector, so it never imports prometheus itself).

func (m *MetricsCollector) IncRunsFetchError(runnerUuid, errorType string) {
	m.runsFetchErrors.WithLabelValues(runnerUuid, errorType).Inc()
}

func (m *MetricsCollector) ObserveRunsFetchDuration(runnerUuid string, seconds float64) {
	m.runsFetchDuration.WithLabelValues(runnerUuid).Observe(seconds)
}

func (m *MetricsCollector) IncJobsCreated(runnerUuid string) {
	m.jobsCreatedTotal.WithLabelValues(runnerUuid).Inc()
}

func (m *MetricsCollector) IncJobCreationError(runnerUuid, errorType string) {
	m.jobCreationErrors.WithLabelValues(runnerUuid, errorType).Inc()
}

func (m *MetricsCollector) ObserveJobCreationDuration(runnerUuid string, seconds float64) {
	m.jobCreationDuration.WithLabelValues(runnerUuid).Observe(seconds)
}

func (m *MetricsCollector) IncJobsAtCapacitySkips(runnerUuid string) {
	m.jobsAtCapacitySkips.WithLabelValues(runnerUuid).Inc()
}

func (m *MetricsCollector) IncServiceAccountsCreated(runnerUuid string) {
	m.serviceAccountsCreatedTotal.WithLabelValues(runnerUuid).Inc()
}

func (m *MetricsCollector) IncServiceAccountCreationError(runnerUuid, errorType string) {
	m.serviceAccountCreationErrors.WithLabelValues(runnerUuid, errorType).Inc()
}

func (m *MetricsCollector) IncDecryptionError(runnerUuid string) {
	m.decryptionErrors.WithLabelValues(runnerUuid).Inc()
}

func (m *MetricsCollector) IncRegistrationSuccess(runnerUuid string) {
	m.runnerRegistrationSuccess.WithLabelValues(runnerUuid).Inc()
}

func (m *MetricsCollector) IncRegistrationError(runnerUuid, errorType string) {
	m.runnerRegistrationErrors.WithLabelValues(runnerUuid, errorType).Inc()
}

func (m *MetricsCollector) IncLoopIteration() {
	m.controllerLoopIterations.Inc()
}

func (m *MetricsCollector) SetActiveRunners(n float64) {
	m.activeRunners.Set(n)
}
