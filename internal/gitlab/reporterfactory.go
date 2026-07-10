package gitlab

import (
	"log/slog"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/report"
)

// ReporterFactory builds a run-scoped report.Reporter for one claimed run (runToken-only
// auth underneath -- the handler never touches the runner's claim credentials). Identical
// seam shape to the manual template (internal/manual/handler.go); kept here per persona
// package rather than shared (P3 -- each persona's factory differs slightly in what it
// also wires per run, e.g. gitlab's decryptor).
type ReporterFactory func(run dispatch.ClaimedRun) report.Reporter

// NewReporterFactory builds the run-scoped ReporterFactory the persona wiring injects
// (identical pattern to internal/manual.NewReporterFactory, 06A §7.1 template).
func NewReporterFactory(baseURL, runnerUuid string, id meshapi.Identity, log *slog.Logger) ReporterFactory {
	return func(run dispatch.ClaimedRun) report.Reporter {
		token := ""
		if run.Details != nil {
			token = run.Details.Spec.RunToken
		}
		rc := meshapi.NewRunClient(baseURL, runnerUuid, meshapi.BearerTokenAuth{Token: token}, meshapi.WithIdentity(id))
		return report.NewReporter(rc, runnerUuid, log.With("run", run.Id))
	}
}
