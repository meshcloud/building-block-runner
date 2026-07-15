package gitlab

import (
	"context"
	"log/slog"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/runmode"
)

// envRunJsonFilePath mirrors runmode.RunJsonFilePathEnv; kept as this package's own name
// because singlerun_test.go still references it directly.
const envRunJsonFilePath = runmode.RunJsonFilePathEnv

// RunSingleRun executes exactly one GITLAB_PIPELINE run from the mounted
// RUN_JSON_FILE_PATH, then returns the process exit code. It reuses the same Handler as
// polling (no loop, no mgmt listener). The controller has already decrypted both the
// run's sensitive inputs AND the pipeline trigger token into the mounted file; the handler
// now reads both as plaintext, and MESHSTACK_RUN is impl-stripped like every other mode.
//
// Exit semantics: exit 0 iff a terminal (or handover) status was reported. register/report
// transport failure -> non-zero (Kotlin exit-1 parity); file missing/parse failure ->
// non-zero (the sanctioned tightening of the Kotlin exit-0 swallow).
func RunSingleRun(ctx context.Context, log *slog.Logger, cfg Config, id meshapi.Identity) int {
	handler := NewHandler(cfg, HandlerDeps{
		Reporters: NewReporterFactory(cfg.Api.Url, cfg.Uuid, id, log),
		Log:       log,
	})

	return runmode.SingleRunFromFile(ctx, log, cfg.Uuid, meshapi.RunnerTypeGitLabPipeline,
		func(ctx context.Context, run dispatch.ClaimedRun) error {
			return handler.Execute(ctx, run)
		})
}
