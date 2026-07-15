package tf

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/meshcloud/building-block-runner/internal/build"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/observability"
	"github.com/meshcloud/building-block-runner/internal/rundecrypt"
)

// NORUN_WORKER_DELAY / FAILED_WORKER_DELAY are the tf type's dispatch.Loop cadence: a 10s idle
// poll and a 60s backoff after a claim-fetch error, frozen from the historic Manager/Worker
// polling loop (deleted -- dispatch.Loop is now the only tf dispatch path) so operators see no
// cadence change. NewClaimClassifier's OutcomeBackoff triggers the latter; see Loop.ClaimBackoff.
const (
	NORUN_WORKER_DELAY  = 10 * time.Second
	FAILED_WORKER_DELAY = 60 * time.Second
)

// tfClaimNodePostfix is the frozen tf fetch node-id suffix (an observable header): the claim
// requester is "<runnerUuid>-worker-1", exactly what the polling Worker sent
// (FetchRunDetails("worker-1")). Kept as a single worker index because the loop's
// maxConcurrentRuns -- not multiple worker goroutines -- now provides concurrency.
const tfClaimNodePostfix = "worker-1"

// NewDispatchRunner assembles the tf type's in-process dispatch stack:
// a dispatch.Loop over an InProcess dispatcher holding the single TERRAFORM handler, driven by
// the tf claim classifier and the frozen "<uuid>-worker-1" node-id. It also performs the opt-in
// self-registration PUT. It does NOT Start the loop or block -- the caller (main) owns the
// Start/signal/drain lifecycle (the ungated type-wiring seam) -- so this assembly stays
// hermetically testable. It is the tf type's only dispatch path: the historic Manager/Worker
// polling loop has been deleted, and this loop is frozen to that loop's cadence (see
// NORUN_WORKER_DELAY/FAILED_WORKER_DELAY) so operators see no scheduling change.
//
// meter and metrics are constructed by the caller (main owns the prometheus registry) and
// injected: meter is the runner_* series (also the loop's StandaloneMetrics for the two additive
// counters), metrics the run_controller_* collector the generic Loop instruments itself with.
func NewDispatchRunner(cfg TfRunnerConfig, logger *slog.Logger, tfBin *TfBinaries, dec Decryptor, meter *observability.RunMetrics, metrics *dispatch.MetricsCollector) (*dispatch.Loop, *dispatch.InProcess, error) {
	auth := cfg.RunApiBackend.NewAuthProvider()

	// Opt-in self-registration: absent registration section => never self-registers.
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
		TfCommandTimeout:      time.Duration(cfg.TfCommandTimeoutMins) * time.Minute,
		InitTimeout:           time.Duration(cfg.InitTimeoutMins) * time.Minute,
		WsTimeout:             time.Duration(cfg.WsTimeoutMins) * time.Minute,
		RunnerUuid:            cfg.RunnerUuid,
		ApiBackend:            cfg.RunApiBackend,
		SkipHostKeyValidation: cfg.SkipHostKeyValidation,
	}, HandlerDeps{
		TfBinaries: tfBin,
		Meter:      meter,
		Log:        logger,
	})

	// ShutdownGraceSeconds is tf's SIGINT/SIGTERM drain window: in-flight runs get this
	// long to finish on their own before InProcess.Wait cancels them and Handler.Execute ->
	// Worker.tfExecution reports a terminal ABORTED (see worker.go).
	shutdownGrace := time.Duration(cfg.ShutdownGraceSeconds) * time.Second
	inproc, err := dispatch.NewInProcess(
		map[meshapi.RunnerImplementationType]dispatch.RunHandler{
			meshapi.RunnerTypeTerraform: rundecrypt.Wrap(handler, dec),
		},
		shutdownGrace, logger.With("component", "dispatch"))
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
