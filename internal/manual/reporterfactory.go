package manual

import (
	"log/slog"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/report"
)

// NewReporterFactory builds the run-scoped ReporterFactory the persona wiring injects: for
// each claimed run it constructs a runToken-only meshapi.RunClient (the run's own token
// from run.Details.Spec.RunToken — the claim credentials never reach the handler, risk #5)
// and wraps it in the event-driven report.Reporter. sourceId/nodeId is the plain runner
// uuid (no worker suffix — plan 05 §16.5); id stamps the additive runner headers (§7.7).
//
// This is the template seam the 06B–D personas copy (their factories differ only in adding
// their external-API client + decryptor). Kept in the persona package so main only wires
// (D11): the handler itself never assembles HTTP transport (handler-purity boundary, §4.1).
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
