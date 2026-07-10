package manual

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

// RunSingleRun executes exactly one MANUAL run from the mounted RUN_JSON_FILE_PATH, then
// returns the process exit code. It reuses the same Handler as polling (no loop, no mgmt
// listener — §7.10). Reporting uses the run's own runToken against cfg.Api.Url (the
// controller injects RUNNER_API_URL; it strips nothing — the k8s trust model is
// unchanged). No decryptor: the controller already decrypted (§2.1.6).
//
// Exit semantics = the 2b-R12 rule (umbrella §7.9): exit 0 iff a terminal status was
// reported. Execute returns nil only after a successful terminal report and non-nil on any
// register/report transport failure or a pre-report failure (missing/unreadable/unparsable
// run file). Consequences: register/update failure ⇒ exit 1 (Kotlin parity); file
// missing/parse failure ⇒ exit 1 — the sanctioned, flagged tightening of the Kotlin exit-0
// swallow (M-P7, umbrella §10.3), so k8s (BackoffLimit:1) retries a run meshStack never
// heard about.
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
		Type:    meshapi.RunnerTypeManual,
		Details: dto,
		RawJson: base64.StdEncoding.EncodeToString(data),
	}

	handler := NewHandler(cfg, HandlerDeps{
		Reporters: NewReporterFactory(cfg.Api.Url, cfg.Uuid, id, log),
		Log:       log,
	})

	if err := handler.Execute(ctx, run); err != nil {
		log.Error("single run failed", "run", run.Id, "err", err)
		return 1
	}
	log.Info("single run completed", "run", run.Id)
	return 0
}
