package manual

import (
	"context"
	"log/slog"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/runmode"
)

// envRunJsonFilePath mirrors runmode.RunJsonFilePathEnv; kept as this package's own name
// because singlerun_test.go / coverage_test.go still reference it directly.
const envRunJsonFilePath = runmode.RunJsonFilePathEnv

// RunSingleRun executes exactly one MANUAL run from the mounted RUN_JSON_FILE_PATH, then
// returns the process exit code. It reuses the same Handler as polling (no loop, no mgmt
// listener). Reporting uses the run's own runToken against cfg.Api.Url (the
// controller injects RUNNER_API_URL; it strips nothing — the k8s trust model is
// unchanged). No decryptor: the controller already decrypted.
//
// Exit semantics: exit 0 iff a terminal status was reported. register/update failure ⇒ exit
// 1 (Kotlin parity); file missing/parse failure ⇒ exit 1 — the deliberate tightening of the
// Kotlin exit-0 swallow, so k8s (BackoffLimit:1) retries a run meshStack never heard about.
func RunSingleRun(ctx context.Context, log *slog.Logger, cfg Config, id meshapi.Identity) int {
	handler := NewHandler(cfg, HandlerDeps{
		Reporters: NewReporterFactory(cfg.Api.Url, cfg.Uuid, id, log),
		Log:       log,
	})

	return runmode.SingleRunFromFile(ctx, log, cfg.Uuid, meshapi.RunnerTypeManual,
		func(ctx context.Context, run dispatch.ClaimedRun) error {
			return handler.Execute(ctx, run)
		})
}
