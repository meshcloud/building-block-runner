package azdevops

import (
	"context"
	"log/slog"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/runmode"
)

// RunSingleRun executes exactly one AZURE_DEVOPS_PIPELINE run from the mounted
// RUN_JSON_FILE_PATH, then returns the process exit code. It reuses Handler as-is: the
// controller has already decrypted the PAT + sensitive inputs at the dispatch boundary,
// so no decryption seam is needed here -- no loop, no mgmt listener. A sync run may
// still hold the Job pod for up to 30 minutes (unchanged from Kotlin).
//
// Exit semantics, with the one sanctioned azdevops delta: exit 0 iff a terminal or handover
// status was reported (async handover, or a sync final/failure update including the pinned
// timeout failure); register/PATCH transport failure exits non-zero (Kotlin exit-1 parity);
// a pre-report file read/parse failure here exits non-zero too -- the sanctioned tightening
// of Kotlin's exit-0 swallow for THIS (outermost, pre-claim) failure surface. A post-register
// implementation-parse failure is instead reported run FAILED by Execute itself and so exits
// 0 (a Go decision, not this function's concern).
func RunSingleRun(ctx context.Context, log *slog.Logger, cfg Config, id meshapi.Identity) int {
	handler := NewHandler(cfg, HandlerDeps{
		Reporters: NewReporterFactory(cfg.Api.Url, cfg.Uuid, id, log),
		Log:       log,
	})

	return runmode.SingleRunFromFile(ctx, log, cfg.Uuid, meshapi.RunnerTypeAzureDevOpsPipeline,
		func(ctx context.Context, run dispatch.ClaimedRun) error {
			return handler.Execute(ctx, run)
		})
}
