package poller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/pkg/auth"
	"github.com/meshcloud/meshstack-cli/pkg/setting"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/metrics"
)

// Register updates the registration of a runner without workload identity.
func Register(ctx context.Context, cfg config.Config) error {
	settings := setting.SingleSource(cfg.Api)
	meshStack, err := newClient(ctx, settings)
	if err != nil {
		return err
	}
	return register(ctx, cfg, settings, meshStack.BuildingBlockRunner, nil)
}

func register(ctx context.Context, cfg config.Config, settings setting.Sources, runners client.MeshBuildingBlockRunnerClient, workloadIdentity *client.MeshRunnerWorkloadIdentityFed) error {
	workspace, err := auth.ResolveWorkspace(ctx, clientOptions(settings))
	if err != nil {
		return err
	}
	if workspace == "" {
		return errors.New("dispatcher.register needs a workspace: set api.auth.workspace, MESHSTACK_WORKSPACE or the default workspace of the profile")
	}
	registration, err := newRegistration(cfg, workspace, workloadIdentity)
	if err != nil {
		return err
	}
	return updateRegistration(ctx, runners, registration)
}

// updateRegistration retries, as meshfed-api may still be starting.
func updateRegistration(ctx context.Context, runners client.MeshBuildingBlockRunnerClient, registration client.MeshBuildingBlockRunner) error {
	ctx, cancel := context.WithTimeoutCause(ctx, meshfedStartTimeout, fmt.Errorf("gave up after %s", meshfedStartTimeout))
	defer cancel()
	runnerUuid := *registration.Metadata.Uuid
	for {
		_, err := runners.Update(ctx, registration)
		if err == nil {
			metrics.RegistrationSuccess.Inc()
			slog.InfoContext(ctx, "updated the registration")
			return nil
		}
		if httpErr, ok := errors.AsType[client.HttpError](err); ok && httpErr.IsNotFound() {
			// meshStack rejects a client-chosen uuid, so the runner cannot create itself.
			err = fmt.Errorf("runner %s does not exist in meshStack, create it in the meshStack UI or API first: %w", runnerUuid, err)
		}
		metrics.RegistrationErrors.Inc()
		slog.WarnContext(ctx, "updating the registration failed, retrying", "retryIn", meshfedStartRetryInterval, "error", err)
		select {
		case <-ctx.Done():
			return fmt.Errorf("update the registration of runner %s (%w): %w", runnerUuid, context.Cause(ctx), err)
		case <-time.After(meshfedStartRetryInterval):
		}
	}
}

func newRegistration(cfg config.Config, workspace string, workloadIdentity *client.MeshRunnerWorkloadIdentityFed) (client.MeshBuildingBlockRunner, error) {
	publicKey, err := cfg.Crypto.FirstPublicKey()
	if err != nil {
		return client.MeshBuildingBlockRunner{}, err
	}
	return client.MeshBuildingBlockRunner{
		Metadata: client.MeshBuildingBlockRunnerMetadata{Uuid: new(cfg.Uuid.String()), OwnedByWorkspace: workspace},
		Spec: client.MeshBuildingBlockRunnerSpec{
			DisplayName:                cfg.Dispatcher.Register.DisplayName,
			PublicKey:                  publicKey,
			ImplementationType:         cfg.Dispatcher.ForTypes[0].ForApi(),
			WorkloadIdentityFederation: workloadIdentity,
		},
	}, nil
}
