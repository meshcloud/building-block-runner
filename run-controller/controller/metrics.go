package controller

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	metricsInstance *MetricsCollector
	metricsOnce     sync.Once
)

// Error type constants for metrics labels
// All error types used in metrics are defined here for discoverability
const (
	// Run fetch error types
	ErrorTypeFetchAPI = "api_error"

	// Job creation error types
	ErrorTypeJobCreation = "job_creation_error"
	ErrorTypeRunTooLarge = "run_too_large"

	// Service account error types
	ErrorTypeServiceAccountCreation = "creation_error"

	// Runner registration error types
	ErrorTypeRegistrationMarshal = "marshal_error"
	ErrorTypeRegistrationPut     = "put_error"
	ErrorTypeRegistrationPost    = "post_error"
)

// MetricsCollector holds all Prometheus metrics for the run-controller
type MetricsCollector struct {
	// Run fetch metrics
	runsFetchErrors   *prometheus.CounterVec
	runsFetchDuration *prometheus.HistogramVec

	// Job creation metrics
	jobsCreatedTotal    *prometheus.CounterVec
	jobCreationErrors   *prometheus.CounterVec
	jobCreationDuration *prometheus.HistogramVec

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

// NewMetricsCollector creates and registers all Prometheus metrics
// Uses singleton pattern to prevent duplicate registration panics
func NewMetricsCollector() *MetricsCollector {
	metricsOnce.Do(func() {
		metricsInstance = &MetricsCollector{
			runsFetchErrors: promauto.NewCounterVec(
				prometheus.CounterOpts{
					Name: "run_controller_runs_fetch_errors_total",
					Help: "Total number of errors while fetching building block runs",
				},
				[]string{"controller_uuid", "error_type"},
			),
			runsFetchDuration: promauto.NewHistogramVec(
				prometheus.HistogramOpts{
					Name:    "run_controller_runs_fetch_duration_seconds",
					Help:    "Duration of run fetch operations in seconds",
					Buckets: prometheus.DefBuckets,
				},
				[]string{"controller_uuid"},
			),
			jobsCreatedTotal: promauto.NewCounterVec(
				prometheus.CounterOpts{
					Name: "run_controller_jobs_created_total",
					Help: "Total number of Kubernetes jobs created for building block runs",
				},
				[]string{"controller_uuid"},
			),
			jobCreationErrors: promauto.NewCounterVec(
				prometheus.CounterOpts{
					Name: "run_controller_job_creation_errors_total",
					Help: "Total number of errors while creating Kubernetes jobs",
				},
				[]string{"controller_uuid", "error_type"},
			),
			jobCreationDuration: promauto.NewHistogramVec(
				prometheus.HistogramOpts{
					Name:    "run_controller_job_creation_duration_seconds",
					Help:    "Duration of Kubernetes job creation operations in seconds",
					Buckets: prometheus.DefBuckets,
				},
				[]string{"controller_uuid"},
			),
			serviceAccountsCreatedTotal: promauto.NewCounterVec(
				prometheus.CounterOpts{
					Name: "run_controller_service_accounts_created_total",
					Help: "Total number of Kubernetes service accounts created for workload identity",
				},
				[]string{"controller_uuid"},
			),
			serviceAccountCreationErrors: promauto.NewCounterVec(
				prometheus.CounterOpts{
					Name: "run_controller_service_account_creation_errors_total",
					Help: "Total number of errors while creating Kubernetes service accounts",
				},
				[]string{"controller_uuid", "error_type"},
			),
			decryptionErrors: promauto.NewCounterVec(
				prometheus.CounterOpts{
					Name: "run_controller_decryption_errors_total",
					Help: "Total number of errors while decrypting run details",
				},
				[]string{"controller_uuid"},
			),
			runnerRegistrationSuccess: promauto.NewCounterVec(
				prometheus.CounterOpts{
					Name: "run_controller_runner_registration_success_total",
					Help: "Total number of successful runner registrations on startup",
				},
				[]string{"controller_uuid"},
			),
			runnerRegistrationErrors: promauto.NewCounterVec(
				prometheus.CounterOpts{
					Name: "run_controller_runner_registration_errors_total",
					Help: "Total number of runner registration errors on startup",
				},
				[]string{"controller_uuid", "error_type"},
			),
			controllerLoopIterations: promauto.NewCounter(
				prometheus.CounterOpts{
					Name: "run_controller_loop_iterations_total",
					Help: "Total number of controller polling loop iterations",
				},
			),
			activeRunners: promauto.NewGauge(
				prometheus.GaugeOpts{
					Name: "run_controller_active_runners",
					Help: "Number of active runners configured in the controller",
				},
			),
		}
	})
	return metricsInstance
}
