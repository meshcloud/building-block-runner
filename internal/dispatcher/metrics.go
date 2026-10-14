package dispatcher

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	errorTypeApi         = "api_error"
	errorTypeStart       = "job_creation_error"
	errorTypeRunTooLarge = "run_too_large"
	errorTypeUpdate      = "put_error"
)

var (
	claimErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "run_controller_runs_fetch_errors_total",
		Help: "Total number of errors while claiming building block runs",
	}, []string{"controller_uuid", "error_type"})
	claimDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "run_controller_runs_fetch_duration_seconds",
		Help:    "Duration of run claims in seconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"controller_uuid"})

	runsStarted = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "run_controller_jobs_created_total",
		Help: "Total number of building block runs started",
	}, []string{"controller_uuid"})
	startErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "run_controller_job_creation_errors_total",
		Help: "Total number of errors while starting building block runs",
	}, []string{"controller_uuid", "error_type"})
	startDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "run_controller_job_creation_duration_seconds",
		Help:    "Duration of run starts in seconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"controller_uuid"})

	atCapacitySkips = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "run_controller_jobs_at_capacity_skips_total",
		Help: "Total number of polling cycles skipped because the runner was at its max concurrent jobs limit",
	}, []string{"controller_uuid"})

	decryptionErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "run_controller_decryption_errors_total",
		Help: "Total number of errors while decrypting run details",
	}, []string{"controller_uuid"})

	registrationSuccess = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "run_controller_runner_registration_success_total",
		Help: "Total number of successful runner registrations on startup",
	}, []string{"controller_uuid"})
	registrationErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "run_controller_runner_registration_errors_total",
		Help: "Total number of runner registration errors on startup",
	}, []string{"controller_uuid", "error_type"})

	loopIterations = promauto.NewCounter(prometheus.CounterOpts{
		Name: "run_controller_loop_iterations_total",
		Help: "Total number of polling loop iterations",
	})
)

func observeDuration(observer prometheus.Observer, since time.Time) {
	observer.Observe(time.Since(since).Seconds())
}
