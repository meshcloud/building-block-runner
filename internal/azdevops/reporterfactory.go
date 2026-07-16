package azdevops

import (
	"log/slog"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/report"
)

// ReporterFactory builds a run-scoped report.Reporter for one claimed run (runToken-only
// auth underneath -- the handler never touches the runner's claim credentials, risk #5).
// Copied from internal/manual.ReporterFactory verbatim in shape; a sibling
// type package must not import another, so this small factory is duplicated per package
// (precedent: manual/outputtype.go).
type ReporterFactory func(run dispatch.ClaimedRun) report.Reporter

// NewReporterFactory builds the ReporterFactory the runner type wiring injects: for each claimed
// run it constructs a runToken-only meshapi.RunClient (the run's own token from
// run.Run.Spec.RunToken) and wraps it in the event-driven report.Reporter. sourceId is
// the plain runner uuid (no worker suffix).
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
