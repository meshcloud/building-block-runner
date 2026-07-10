package gitlab

import (
	"context"
	"encoding/base64"
	"log/slog"
	"os"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
)

// envRunJsonFilePath is the frozen k8s single-run contract: the controller mounts the run
// JSON here (default /var/run/secrets/meshstack/run.json) and injects the path (D9/§7.3).
const envRunJsonFilePath = "RUN_JSON_FILE_PATH"

// RunSingleRun executes exactly one GITLAB_PIPELINE run from the mounted
// RUN_JSON_FILE_PATH, then returns the process exit code. It reuses the same Handler as
// polling (no loop, no mgmt listener -- §7.10). The NoOp decryptor is used because the
// controller has already decrypted both the run's sensitive inputs AND the pipeline
// trigger token into the mounted file (decryption.go:27-39,86-92) -- meshapi.DecryptInputs
// degenerates to an identity transform, and the k8s caveat applies: MESHSTACK_RUN embeds
// the plaintext trigger token in single-run mode, pinned as-is (§2.6, G-P12).
//
// Exit semantics = the 2b-R12 rule (umbrella §7.9): exit 0 iff a terminal (or handover)
// status was reported. register/report transport failure -> non-zero (Kotlin exit-1
// parity); file missing/parse failure -> non-zero (the sanctioned, flagged tightening of
// the Kotlin exit-0 swallow, G-P13, umbrella §10.3).
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
		Type:    meshapi.RunnerTypeGitLabPipeline,
		Details: dto,
		RawJson: base64.StdEncoding.EncodeToString(data),
	}

	handler := NewHandler(cfg, HandlerDeps{
		Reporters: NewReporterFactory(cfg.Api.Url, cfg.Uuid, id, log),
		Decryptor: meshapi.NoopDecryptor{},
		HTTP:      noFollowRedirectClient(),
		Log:       log,
	})

	if err := handler.Execute(ctx, run); err != nil {
		log.Error("single run failed", "run", run.Id, "err", err)
		return 1
	}
	log.Info("single run completed", "run", run.Id)
	return 0
}
