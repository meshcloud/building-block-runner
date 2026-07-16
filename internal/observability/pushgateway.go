package observability

import (
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"
)

// EnvPushGatewayURL is the opt-in trigger for pushing a single run's metrics to a
// Prometheus push gateway before the process exits (docs/ARCHITECTURE.md §6.1): a
// Job-dispatched single run serves no /metrics listener and exits before Prometheus could
// ever scrape it, so its per-run execution metrics have no exposition path unless pushed.
// Unset (the default) disables push entirely -- a Job-dispatched single run behaves
// exactly as it did before this feature existed: no listener, no push, its metrics simply
// never leave the process. New config surface, documented in docs/DEPRECATIONS.md.
const EnvPushGatewayURL = "PUSH_GATEWAY_URL"

// pushJobName is the Pushgateway "job" grouping-key label every single-run push uses. The
// pushing type/identity already appears inside the series themselves (runner_uuid label)
// and in the "run_id" grouping label PushRunMetrics adds per call, so one constant job
// name keeps every type's pushed groups under a single, predictable Pushgateway job
// rather than growing a per-type name that would need its own frozen-contract entry.
const pushJobName = "bbrunner_single_run"

// pushTimeout bounds every HTTP call PushRunMetrics makes to the gateway. A Kubernetes
// Job runs with BackoffLimit:1/RestartPolicy:Never (docs/ARCHITECTURE.md §4): the process
// must exit promptly whether or not the gateway is reachable, so a slow or unreachable
// gateway must never hang Job completion.
const pushTimeout = 5 * time.Second

// PushGatewayURL reports the configured push-gateway endpoint, or "" if push-gateway
// support is disabled (the default: PUSH_GATEWAY_URL unset). Off-by-default means no
// behavior change for any existing deployment that has not opted in.
func PushGatewayURL() string {
	return os.Getenv(EnvPushGatewayURL)
}

// PushRunMetrics pushes reg's collected series to url under a grouping key scoped to this
// one run, so distinct runs' pushed groups are distinguishable and individually deletable
// rather than clobbering each other. It is a no-op if url is "" (push-gateway support
// disabled, the default).
//
// The grouping key carries only run_id, not runner_uuid: every series RunMetrics produces
// already carries its own runner_uuid label (the frozen runner_* metric contract, §4), and
// the Pushgateway client rejects a push whose grouping key repeats a label name a pushed
// metric already carries ("pushed metric ... already contains grouping label runner_uuid")
// -- run_id alone (a per-run UUID) is already globally unique, so the combination of the
// job name, the run_id grouping label and each series' own runner_uuid label still makes
// every pushed group fully attributable to one runner_uuid + run id, matching the
// docs/ARCHITECTURE.md §6.1 intent without violating that constraint.
//
// success selects delete-on-success semantics: once a successful run's series have been
// pushed, its group is deleted from the gateway right away -- Prometheus need only scrape
// a successful run's series once, and a Job-dispatched single run never runs again under
// the same run id, so nothing would ever come back to scrape a lingering group. A failed
// run's group is deliberately left on the gateway for an operator to inspect.
//
// Every gateway call is bounded by pushTimeout (set on the Pusher's HTTP client, so it
// covers both the push and the delete) so a slow or unreachable gateway can never block
// process exit. Any error is logged, never fatal -- push-gateway support is best-effort
// observability, not part of the run's own success/failure contract.
func PushRunMetrics(log *slog.Logger, url, runnerUuid, runId string, reg prometheus.Gatherer, success bool) {
	if url == "" {
		return
	}

	pusher := push.New(url, pushJobName).
		Client(&http.Client{Timeout: pushTimeout}).
		Grouping("run_id", runId).
		Gatherer(reg)

	if err := pusher.Push(); err != nil {
		log.Warn("failed to push run metrics to push gateway", "url", url, "runnerUuid", runnerUuid, "runId", runId, "error", err)
		return
	}

	if success {
		if err := pusher.Delete(); err != nil {
			log.Warn("failed to delete run metrics group from push gateway", "url", url, "runnerUuid", runnerUuid, "runId", runId, "error", err)
		}
	}
}

// InstrumentSingleRun wraps a single-run execution with per-run Prometheus
// instrumentation: it builds a fresh registry, records fn's outcome (success/failure plus
// duration) on a runner_* RunMetrics bound to runnerUuid, and -- if push-gateway support
// is enabled (PushGatewayURL) -- pushes that run's series to the gateway before returning
// (see PushRunMetrics). fn's own error is returned unchanged; this function only ever adds
// observability around fn, never changes its outcome.
//
// This assumes fn's error doubles as the run's success/failure signal, true for every runner
// type's single-run Execute except tf's (tf's engine only errors on pre-flight failures,
// so a failed tofu apply/destroy is not itself an error) -- tf's single-run path uses
// InstrumentSingleRunResult instead, via its Meter's Push method (see cmd/tf/singlerunmeter.go).
func InstrumentSingleRun(log *slog.Logger, runnerUuid, runId string, fn func() error) error {
	return InstrumentSingleRunResult(log, runnerUuid, runId, func() (bool, error) {
		err := fn()
		return err == nil, err
	})
}

// InstrumentSingleRunResult is InstrumentSingleRun's more general form: fn reports its outcome as
// two independent signals -- an error (returned unchanged, drives the process exit code) and a
// success flag (drives the runner_runs_succeeded/failed metric and PushRunMetrics'
// delete-on-success) -- for callers whose error cannot double as the success signal (B5: tf's
// engine only errors on pre-flight failures, so a failed tofu apply/destroy must still be
// metered as failed even though the engine itself returns a nil error).
func InstrumentSingleRunResult(log *slog.Logger, runnerUuid, runId string, fn func() (bool, error)) error {
	reg := NewRegistry()
	meter := NewRunMetrics(reg, runnerUuid)

	start := time.Now()
	success, err := fn()
	d := time.Since(start)

	if success {
		meter.RunSucceeded(d)
	} else {
		meter.RunFailed(d)
	}

	PushRunMetrics(log, PushGatewayURL(), runnerUuid, runId, reg, success)
	return err
}
