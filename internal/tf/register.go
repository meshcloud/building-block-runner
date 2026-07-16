package tf

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

// NORUN_WORKER_DELAY / FAILED_WORKER_DELAY are the tf type's dispatch.Loop cadence: a 10s idle
// poll and a 60s backoff after a claim-fetch error, frozen from the historic Manager/Worker
// polling loop (deleted -- dispatch.Loop is now the only tf dispatch path) so operators see no
// cadence change. NewClaimClassifier's OutcomeBackoff triggers the latter; see Loop.ClaimBackoff.
// The dispatch-loop assembly that wires these into LoopConfig lives in cmd/bbrunner/tf.go (package
// main), mirroring the other four runner types; the type package owns only the frozen values.
const (
	NORUN_WORKER_DELAY  = 10 * time.Second
	FAILED_WORKER_DELAY = 60 * time.Second
)

// ClaimNodePostfix is the frozen tf fetch node-id suffix (an observable header): the claim
// requester is "<runnerUuid>-worker-1", exactly what the polling Worker sent
// (FetchRunDetails("worker-1")). Kept as a single worker index because the loop's
// maxConcurrentRuns -- not multiple worker goroutines -- now provides concurrency. The assembly
// in cmd/bbrunner/tf.go wires it via dispatch.WithRequester.
const ClaimNodePostfix = "worker-1"

// Register performs the opt-in standalone tf self-registration PUT. It is a
// no-op (returns nil) when cfg.Registration is absent -- the standalone tf runner never self-
// registers by default, exactly as today. When present it PUTs a WIF-less
// MeshBuildingBlockRunnerDTO (a standalone has no projected tokens) for the configured
// capability; a 404 maps to the frozen "create it via the meshStack UI" contract.
func Register(logger *slog.Logger, cfg TfRunnerConfig, auth meshapi.AuthProvider) error {
	reg := cfg.Registration
	if reg == nil {
		return nil
	}

	capability, err := dispatch.ParseCapability(reg.Capability)
	if err != nil {
		return fmt.Errorf("invalid registration.capability: %w", err)
	}

	dto := meshapi.MeshBuildingBlockRunnerDTO{
		ApiVersion: "v1-preview",
		Kind:       "meshBuildingBlockRunner",
		Metadata: meshapi.MeshBuildingBlockRunnerMetaDTO{
			Uuid:             cfg.Uuid,
			OwnedByWorkspace: reg.OwnedByWorkspace,
		},
		Spec: meshapi.MeshBuildingBlockRunnerSpecDTO{
			DisplayName:        reg.DisplayName,
			PublicKey:          reg.PublicKey,
			ImplementationType: capability.String(),
			// No WorkloadIdentityFederation: a standalone tf runner has no projected tokens.
		},
	}

	body, err := json.Marshal(dto)
	if err != nil {
		return fmt.Errorf("failed to marshal tf runner registration: %w", err)
	}

	logger.Info("registering tf runner", "uuid", cfg.Uuid, "capability", capability)
	statusCode, err := meshapi.NewRunnerClient(cfg.Api.Url, auth).Update(cfg.Uuid, body)
	if err != nil {
		return fmt.Errorf("tf runner registration failed: %w", err)
	}
	if statusCode == http.StatusNotFound {
		return fmt.Errorf("runner %s not found in meshfed — create it via the meshStack UI or API before starting the tf-block-runner", cfg.Uuid)
	}

	logger.Info("tf runner registered successfully", "uuid", cfg.Uuid)
	return nil
}
