package metrics

import (
	"math"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/meshcloud/building-block-runner/internal/build"
	"github.com/meshcloud/building-block-runner/internal/config"
)

const (
	ErrorTypeStart       = "start_failed"
	ErrorTypeRunTooLarge = "run_too_large"
	ErrorTypeDecryption  = "decryption_failed"
	ErrorTypeWifToken    = "wif_token"
)

const (
	ResultSucceeded = "succeeded"
	ResultFailed    = "failed"
)

const (
	metricPrefix = "building_block_runner_"
	infoName     = metricPrefix + "info"
)

const (
	labelUuid      = "uuid"
	labelVersion   = "version"
	labelErrorType = "error_type"
	labelType      = "type"
	labelResult    = "result"
)

var (
	shortDurationBuckets = prometheus.ExponentialBuckets(0.001, 2, 14)
	runDurationBuckets   = []float64{1, 5, 15, 30, 60, 120, 300, 600, 1200, 1800, 3600}
)

var registry = prometheus.NewRegistry()

var factory = promauto.With(registry)

func init() {
	registry.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
}

var info = factory.NewGaugeVec(prometheus.GaugeOpts{
	Name: infoName,
	Help: "Always 1, with the uuid and version of the runner as labels",
}, []string{labelUuid, labelVersion})

var (
	ActiveRuns = factory.NewGauge(prometheus.GaugeOpts{
		Name: metricPrefix + "active_runs",
		Help: "Number of building block runs that the runner started and that have not finished yet",
	})
	maxConcurrentRuns = factory.NewGauge(prometheus.GaugeOpts{
		Name: metricPrefix + "max_concurrent_runs",
		Help: "Number of building block runs that the runner runs at most at the same time, +Inf when unlimited",
	})

	ClaimErrors = factory.NewCounter(prometheus.CounterOpts{
		Name: metricPrefix + "claim_errors_total",
		Help: "Total number of errors while claiming building block runs",
	})
	ClaimDuration = factory.NewHistogram(prometheus.HistogramOpts{
		Name:    metricPrefix + "claim_duration_seconds",
		Help:    "Duration of run claims in seconds",
		Buckets: shortDurationBuckets,
	})

	RunsStarted = factory.NewCounter(prometheus.CounterOpts{
		Name: metricPrefix + "runs_started_total",
		Help: "Total number of building block runs started",
	})
	StartErrors = factory.NewCounterVec(prometheus.CounterOpts{
		Name: metricPrefix + "start_errors_total",
		Help: "Total number of errors while decrypting or starting building block runs",
	}, []string{labelErrorType})
	StartDuration = factory.NewHistogram(prometheus.HistogramOpts{
		Name:    metricPrefix + "start_duration_seconds",
		Help:    "Duration of run starts in seconds",
		Buckets: shortDurationBuckets,
	})
	RunDuration = factory.NewHistogramVec(prometheus.HistogramOpts{
		Name:    metricPrefix + "run_duration_seconds",
		Help:    "Duration of building block runs from their start until the runner sees them finish, in seconds",
		Buckets: runDurationBuckets,
	}, []string{labelType, labelResult})

	RegistrationSuccess = factory.NewCounter(prometheus.CounterOpts{
		Name: metricPrefix + "registrations_total",
		Help: "Total number of successful runner registrations on startup",
	})
	RegistrationErrors = factory.NewCounter(prometheus.CounterOpts{
		Name: metricPrefix + "registration_errors_total",
		Help: "Total number of runner registration errors on startup",
	})

	ServiceAccountsCreated = factory.NewCounter(prometheus.CounterOpts{
		Name: metricPrefix + "service_accounts_created_total",
		Help: "Total number of Kubernetes service accounts created for workload identity",
	})
	ServiceAccountErrors = factory.NewCounter(prometheus.CounterOpts{
		Name: metricPrefix + "service_account_errors_total",
		Help: "Total number of errors while creating Kubernetes service accounts",
	})
)

func setFromConfig(cfg config.Config) {
	info.WithLabelValues(cfg.Uuid.String(), build.Version).Set(1)
	if limit, limited := cfg.Dispatcher.MaxConcurrentRuns(); limited {
		maxConcurrentRuns.Set(float64(limit))
	} else {
		maxConcurrentRuns.Set(math.Inf(1))
	}
}
