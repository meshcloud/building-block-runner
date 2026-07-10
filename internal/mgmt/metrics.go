package mgmt

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// RunMetrics is the D12 generic standalone-runner instrumentation: runs claimed,
// succeeded, failed, run duration, and poll errors, each labeled by runner_uuid.
// Every persona that lacks its own equivalent metrics wires this in (the run-controller
// persona does not -- its run_controller_* series already covers the same events,
// making a duplicate runner_* series dashboard noise, plan-04 §10.5).
//
// RunMetrics structurally satisfies whatever small consumer-side "meter" interface a
// polling loop declares for itself (P3 -- e.g. internal/tf.Meter); this package does not
// import a persona package to enforce that, keeping the dependency direction adapters-do-
// not-flow-into-domain intact.
type RunMetrics struct {
	claimed        *prometheus.CounterVec
	succeeded      *prometheus.CounterVec
	failed         *prometheus.CounterVec
	duration       *prometheus.HistogramVec
	pollErrors     *prometheus.CounterVec
	unhandled      *prometheus.CounterVec
	atCapacitySkip *prometheus.CounterVec
	runnerUuid     string
}

// NewRunMetrics registers the runner_* series against reg and returns a RunMetrics bound
// to runnerUuid. reg is a prometheus.Registerer, not necessarily the process-default
// registry (see NewRegistry) -- tests use a fresh one per case to avoid duplicate-
// registration panics across parallel subtests.
func NewRunMetrics(reg prometheus.Registerer, runnerUuid string) *RunMetrics {
	factory := promauto.With(reg)
	labels := []string{"runner_uuid"}
	return &RunMetrics{
		claimed: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "runner_runs_claimed_total",
			Help: "Total number of building block runs this runner claimed.",
		}, labels),
		succeeded: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "runner_runs_succeeded_total",
			Help: "Total number of building block runs this runner completed successfully.",
		}, labels),
		failed: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "runner_runs_failed_total",
			Help: "Total number of building block runs this runner completed with a failure.",
		}, labels),
		duration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "runner_run_duration_seconds",
			Help:    "Duration of a claimed run's execution, from claim to terminal status.",
			Buckets: prometheus.DefBuckets,
		}, labels),
		pollErrors: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "runner_poll_errors_total",
			Help: "Total number of unexpected errors while polling/claiming a run.",
		}, labels),
		unhandled: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "runner_runs_unhandled_total",
			Help: "Total number of claimed runs this runner had no handler for (fail-fast, D5). Distinct from runner_runs_failed_total, which counts runs that executed and failed.",
		}, []string{"runner_uuid", "type"}),
		atCapacitySkip: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "runner_at_capacity_skips_total",
			Help: "Total number of polling cycles skipped because the runner was at its max concurrent runs limit (the in-process twin of run_controller_jobs_at_capacity_skips_total).",
		}, labels),
		runnerUuid: runnerUuid,
	}
}

// RunClaimed records a successful claim (fetch) of a run.
func (m *RunMetrics) RunClaimed() {
	m.claimed.WithLabelValues(m.runnerUuid).Inc()
}

// RunSucceeded records a run that reached a successful terminal status, with its
// execution duration (claim to terminal status).
func (m *RunMetrics) RunSucceeded(d time.Duration) {
	m.succeeded.WithLabelValues(m.runnerUuid).Inc()
	m.duration.WithLabelValues(m.runnerUuid).Observe(d.Seconds())
}

// RunFailed records a run that reached a failed terminal status, with its execution
// duration (claim to terminal status).
func (m *RunMetrics) RunFailed(d time.Duration) {
	m.failed.WithLabelValues(m.runnerUuid).Inc()
	m.duration.WithLabelValues(m.runnerUuid).Observe(d.Seconds())
}

// PollError records a non-norun error while fetching/claiming a run -- i.e. neither "no
// run available" nor a benign transport quirk (see internal/tf's handleFetchRunError).
func (m *RunMetrics) PollError() {
	m.pollErrors.WithLabelValues(m.runnerUuid).Inc()
}

// RunUnhandled records a claimed run whose type this runner had no handler for (D5 claim-
// and-fail-fast, PLAN_DETAIL_05 §10.1). It is deliberately NOT a runner_runs_failed_total
// (that series means "executed and failed", plan §16 table row "unhandled type").
func (m *RunMetrics) RunUnhandled(runnerType string) {
	m.unhandled.WithLabelValues(m.runnerUuid, runnerType).Inc()
}

// AtCapacitySkip records a polling cycle skipped because the runner was already at its
// configured max concurrent runs -- the standalone in-process twin of the controller's
// run_controller_jobs_at_capacity_skips_total (PLAN_DETAIL_05 §6/§16).
func (m *RunMetrics) AtCapacitySkip() {
	m.atCapacitySkip.WithLabelValues(m.runnerUuid).Inc()
}
