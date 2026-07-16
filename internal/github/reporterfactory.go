package github

import (
	"log/slog"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/report"
)

// NewReporterFactory builds the run-scoped ReporterFactory the runner type wiring injects:
// for each claimed run it constructs a runToken-only meshapi.RunClient
// (the run's own token — the claim credentials never reach the handler) wrapped in
// the event-driven report.Reporter. sourceId/nodeId is the plain runner uuid; id stamps the
// additive runner headers.
func NewReporterFactory(baseURL, runnerUuid string, id meshapi.Identity, log *slog.Logger) ReporterFactory {
	return func(run dispatch.ClaimedRun) report.Reporter {
		token := ""
		if run.Run != nil {
			token = run.Run.Spec.RunToken
		}
		rc := meshapi.NewRunClient(baseURL, runnerUuid, meshapi.BearerTokenAuth{Token: token}, meshapi.WithIdentity(id))
		return report.NewReporter(rc, runnerUuid, log.With("runId", run.Id))
	}
}
