package tf

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/meshcloud/building-block-runner/internal/build"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/mgmt"
)

// tfClaimNodePostfix is the frozen tf fetch node-id suffix (D9 observable header): the claim
// requester is "<runnerUuid>-worker-1", exactly what the polling Worker sent
// (FetchRunDetails("worker-1")). Kept as a single worker index because the loop's
// maxConcurrentRuns -- not multiple worker goroutines -- now provides concurrency.
const tfClaimNodePostfix = "worker-1"

// NewDispatchRunner assembles the tf persona's in-process dispatch stack (PLAN_DETAIL_05 §6):
// a dispatch.Loop over an InProcess dispatcher holding the single TERRAFORM handler, driven by
// the tf claim classifier and the frozen "<uuid>-worker-1" node-id. It also performs the opt-in
// self-registration PUT (§9). It does NOT Start the loop or block -- the caller (main) owns the
// Start/signal/drain lifecycle (the ungated persona-wiring seam) -- so this assembly stays
// hermetically testable. It is the in-process replacement for the Manager/Worker polling loop;
// mains select it opt-in via RUNNER_DISPATCHER and keep the Manager as the default until full
// characterization-through-loop equivalence is proven (run-log addendum).
//
// meter and metrics are constructed by the caller (main owns the prometheus registry) and
// injected: meter is the runner_* series (also the loop's StandaloneMetrics for the two additive
// counters), metrics the run_controller_* collector the generic Loop instruments itself with.
func NewDispatchRunner(cfg TfRunnerConfig, logger *slog.Logger, tfBin *TfBinaries, dec Decryptor, meter *mgmt.RunMetrics, metrics *dispatch.MetricsCollector) (*dispatch.Loop, *dispatch.InProcess, error) {
	auth := cfg.RunApiBackend.NewAuthProvider()

	// Opt-in self-registration (§9): absent registration section => never self-registers.
	if err := Register(logger, cfg, auth); err != nil {
		return nil, nil, fmt.Errorf("tf runner registration failed: %w", err)
	}

	identity := meshapi.Identity{Name: "tf-block-runner", Version: build.Version}
	claimClient := dispatch.NewRunClaimClient(
		cfg.RunApiBackend.Url, cfg.RunnerUuid, "", auth, identity, metrics,
		dispatch.WithRequester(func(uuid string) string { return uuid + "-" + tfClaimNodePostfix }),
	)

	handler := NewHandler(HandlerConfig{
		WorkingDir:            cfg.TfParentWorkingDir,
		TfCommandTimeoutMins:  cfg.TfCommandTimeoutMins,
		InitTimeoutMins:       cfg.InitTimeoutMins,
		WsTimeoutMins:         cfg.WsTimeoutMins,
		RunnerUuid:            cfg.RunnerUuid,
		ApiBackend:            cfg.RunApiBackend,
		SkipHostKeyValidation: cfg.SkipHostKeyValidation,
	}, HandlerDeps{
		TfBinaries: tfBin,
		Decryptor:  dec,
		Meter:      meter,
		Log:        logger,
	})

	inproc, err := dispatch.NewInProcess(
		map[meshapi.RunnerImplementationType]dispatch.RunHandler{meshapi.RunnerTypeTerraform: handler},
		0, logger.With("component", "dispatch"))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build in-process dispatcher: %w", err)
	}

	// Ensure the working dir exists (the Manager did this in Start).
	if err := os.MkdirAll(cfg.TfParentWorkingDir, 0o777); err != nil {
		logger.Error("failed to create working directory", "dir", cfg.TfParentWorkingDir, "error", err)
	}

	loop := dispatch.NewLoop(dispatch.LoopConfig{
		// PollInterval == the old NORUN_WORKER_DELAY (10s idle poll); ClaimBackoff == the old
		// FAILED_WORKER_DELAY (60s after a fetch error) -- see NewClaimClassifier.
		PollInterval:  NORUN_WORKER_DELAY,
		ClaimBackoff:  FAILED_WORKER_DELAY,
		MaxConcurrent: cfg.MaxConcurrentRuns,
	}, dispatch.LoopDeps{
		RunnerUuid: cfg.RunnerUuid,
		Claimer:    claimClient,
		Dispatcher: inproc,
		StatusApi:  claimClient,
		Classify:   NewClaimClassifier(meter),
		Metrics:    metrics,
		Standalone: meter,
		Logger:     logger.With("component", "dispatch"),
	})

	return loop, inproc, nil
}

// Register performs the opt-in standalone tf self-registration PUT (PLAN_DETAIL_05 §9). It is a
// no-op (returns nil) when cfg.Registration is absent -- the standalone tf runner never self-
// registers by default (§3.2), exactly as today. When present it PUTs a WIF-less
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
			Uuid:             cfg.RunnerUuid,
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

	logger.Info("registering tf runner", "uuid", cfg.RunnerUuid, "capability", capability)
	statusCode, err := meshapi.NewRunnerClient(cfg.RunApiBackend.Url, auth).Update(cfg.RunnerUuid, body)
	if err != nil {
		return fmt.Errorf("tf runner registration failed: %w", err)
	}
	if statusCode == http.StatusNotFound {
		return fmt.Errorf("runner %s not found in meshfed — create it via the meshStack UI or API before starting the tf-block-runner", cfg.RunnerUuid)
	}

	logger.Info("tf runner registered successfully", "uuid", cfg.RunnerUuid)
	return nil
}
