package github

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

// RunSingleRun executes exactly one GITHUB_WORKFLOW run from RUN_JSON_FILE_PATH, then
// returns the process exit code. The run JSON is ALREADY decrypted (the controller
// pre-decrypted appPem + inputs), so the handler needs no decryptor at all. No loop, no mgmt
// listener; reporting uses the run's own runToken against cfg.Api.Url. Exit semantics: exit
// 0 iff a terminal OR IN_PROGRESS-handover update was reported (Execute returns nil in both
// cases — for async single-run the handover IS the job's success). Pre-report fetch/parse
// failures exit non-zero: the sanctioned tightening of the Kotlin exit-0 swallow, so k8s
// retries a run meshStack never heard about.
func RunSingleRun(ctx context.Context, log *slog.Logger, cfg Config, id meshapi.Identity) int {
	handler := NewHandler(cfg, HandlerDeps{
		Reporters: NewReporterFactory(cfg.Api.Url, cfg.Uuid, id, log),
		Log:       log,
	})

	return runmode.SingleRunFromFile(ctx, log, cfg.Uuid, meshapi.RunnerTypeGitHubWorkflow,
		func(ctx context.Context, run dispatch.ClaimedRun) error {
			return handler.Execute(ctx, run)
		})
}
