package azdevops

import (
	"context"
	"encoding/base64"
	"log/slog"
	"os"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
)

// envRunJsonFilePath is the frozen k8s single-run contract (D9/§7.3, 06A §2.3 pattern).
const envRunJsonFilePath = "RUN_JSON_FILE_PATH"

// RunSingleRun executes exactly one AZURE_DEVOPS_PIPELINE run from the mounted
// RUN_JSON_FILE_PATH, then returns the process exit code. It reuses Handler with a NoOp
// decryptor (the controller has already decrypted the PAT + sensitive inputs,
// decryption.go:97-111) -- no loop, no mgmt listener (§7.10). A sync run may still hold the
// Job pod for up to 30 minutes (unchanged from Kotlin, §7.2).
//
// Exit semantics are the R12 rule (umbrella §7.9), with the one sanctioned azdevops delta
// (K-P2, §7.2): exit 0 iff a terminal or handover status was reported (async handover, or a
// sync final/failure update including the pinned timeout failure); register/PATCH transport
// failure exits non-zero (Kotlin exit-1 parity); a pre-report file read/parse failure here
// exits non-zero too -- the sanctioned tightening of Kotlin's exit-0 swallow for THIS
// (outermost, pre-claim) failure surface. A post-register implementation-parse failure is
// instead reported run FAILED by Execute itself and so exits 0 (§4.5's Go decision, not this
// function's concern).
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
		Type:    meshapi.RunnerTypeAzureDevOpsPipeline,
		Details: dto,
		RawJson: base64.StdEncoding.EncodeToString(data),
	}

	handler := NewHandler(cfg, HandlerDeps{
		Reporters: NewReporterFactory(cfg.Api.Url, cfg.Uuid, id, log),
		Decryptor: meshapi.NoopDecryptor{},
		Log:       log,
	})

	if err := handler.Execute(ctx, run); err != nil {
		log.Error("single run failed", "run", run.Id, "err", err)
		return 1
	}
	log.Info("single run completed", "run", run.Id)
	return 0
}
