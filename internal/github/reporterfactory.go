package github

import (
	"log/slog"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/report"
)

// NewReporterFactory builds the run-scoped ReporterFactory the persona wiring injects (the
// 06A template seam): for each claimed run it constructs a runToken-only meshapi.RunClient
// (the run's own token — the claim credentials never reach the handler, risk #5) wrapped in
// the event-driven report.Reporter. sourceId/nodeId is the plain runner uuid; id stamps the
// additive runner headers (§7.7).
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
