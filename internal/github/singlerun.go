package github

import (
	"context"
	"encoding/base64"
	"log/slog"
	"os"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
)

// envRunJsonFilePath is the frozen k8s single-run contract: the controller mounts the run
// JSON here and injects the path (D9/§7). The run JSON is ALREADY decrypted (the controller
// pre-decrypted appPem + inputs, D7), so single-run uses a NoOp decryptor.
const envRunJsonFilePath = "RUN_JSON_FILE_PATH"

// RunSingleRun executes exactly one GITHUB_WORKFLOW run from RUN_JSON_FILE_PATH, then
// returns the process exit code. No loop, no mgmt listener (§7.10); reporting uses the run's
// own runToken against cfg.Api.Url. Exit semantics = the R12 rule (§7, G-P11): exit 0 iff a
// terminal OR IN_PROGRESS-handover update was reported (Execute returns nil in both cases —
// for async single-run the handover IS the job's success). Pre-report fetch/parse failures
// exit non-zero: the sanctioned, flagged tightening of the Kotlin exit-0 swallow (§7.9),
// so k8s retries a run meshStack never heard about.
func RunSingleRun(ctx context.Context, log *slog.Logger, cfg Config, id meshapi.Identity) int {
	path := os.Getenv(envRunJsonFilePath)
	if path == "" {
		log.Error("single-run mode requires RUN_JSON_FILE_PATH")
		return 1
	}

	data, err := os.ReadFile(path)
	if err != nil {
		log.Error("failed to read run JSON file", "path", path, "err", err)
		return 1
	}

	dto, err := meshapi.ParseRunDetails(data)
	if err != nil {
		log.Error("failed to parse run JSON", "path", path, "err", err)
		return 1
	}

	run := dispatch.ClaimedRun{
		Id:      dispatch.RunId(dto.Metadata.Uuid),
		Type:    meshapi.RunnerTypeGitHubWorkflow,
		Details: dto,
		RawJson: base64.StdEncoding.EncodeToString(data),
	}

	handler := NewHandler(cfg, HandlerDeps{
		Reporters: NewReporterFactory(cfg.Api.Url, cfg.Uuid, id, log),
		Decryptor: NoOpDecryptor{}, // controller already decrypted (D7)
		Log:       log,
	})

	if err := handler.Execute(ctx, run); err != nil {
		log.Error("single run failed", "run", run.Id, "err", err)
		return 1
	}
	log.Info("single run completed", "run", run.Id)
	return 0
}
