package poller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/client/types/enum"
	"github.com/meshcloud/meshstack-cli/pkg/auth"
	"github.com/meshcloud/meshstack-cli/pkg/setting"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/metrics"
)

var errCannotRegister = errors.New("cannot register the runner")

func register(ctx context.Context, cfg config.Config, sources setting.Sources, runners client.MeshBuildingBlockRunnerClient, workloadIdentity *client.MeshRunnerWorkloadIdentityFed) error {
	workspace, err := auth.ResolveWorkspace(ctx, auth.ResolveClientOptions{SettingSources: sources})
	if err != nil {
		return err
	}
	if workspace == "" {
		return fmt.Errorf("%w: dispatcher.register needs a workspace: set meshstack.workspace in runner-config.yml, MESHSTACK_WORKSPACE or the default workspace of the profile", errCannotRegister)
	}
	_, err = runners.Update(ctx, newRegistration(cfg, workspace, workloadIdentity))
	if httpErr, ok := errors.AsType[client.HttpError](err); ok && httpErr.IsNotFound() {
		// meshStack rejects a client-chosen uuid, so the runner cannot create itself.
		err = fmt.Errorf("%w: runner %s does not exist in meshStack, create it in the meshStack UI or API first: %w", errCannotRegister, cfg.Uuid, err)
	}
	if err != nil {
		metrics.RegistrationErrors.Inc()
		return fmt.Errorf("update the registration: %w", err)
	}
	metrics.RegistrationSuccess.Inc()
	slog.InfoContext(ctx, "updated the registration", "runner", cfg.Uuid, "workspace", workspace)
	return nil
}

func newRegistration(cfg config.Config, workspace string, workloadIdentity *client.MeshRunnerWorkloadIdentityFed) client.MeshBuildingBlockRunner {
	return client.MeshBuildingBlockRunner{
		Metadata: client.MeshBuildingBlockRunnerMetadata{Uuid: new(cfg.Uuid.String()), OwnedByWorkspace: workspace},
		Spec: client.MeshBuildingBlockRunnerSpec{
			DisplayName:                cfg.Dispatcher.Register.DisplayName,
			PublicKey:                  cfg.Crypto.PublicKey(),
			ImplementationType:         enum.Entry[client.MeshBuildingBlockRunnerImplementationType](cfg.Dispatcher.ForTypes[0].ForApi()),
			WorkloadIdentityFederation: workloadIdentity,
		},
	}
}
