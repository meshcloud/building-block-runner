package testacc

import (
	"context"
	"testing"
	"uuid"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/config/xurl"
	"github.com/meshcloud/building-block-runner/internal/dispatcher"
)

// One every local stack has: the dev dump's ALL runner belongs to it.
const workspace = "admin-customer"

// TestAccRunner drives a runner of its own, which no runner process of the local stack polls for,
// so its claims reach the code under test and nothing else.
func TestAccRunner(t *testing.T) {
	stack := requireLocalStack(t)
	runner := stack.createRunner(t)

	steps := []struct {
		name string
		step func(*testing.T)
	}{
		{"the registration updates the runner", registrationUpdatesTheRunner(stack, runner)},
		{"a claim without work finds no run and marks the runner as seen", claimWithoutWorkFindsNoRun(stack, runner)},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.step) {
			t.FailNow()
		}
	}
}

func (s localStack) createRunner(t *testing.T) config.Config {
	t.Helper()
	// meshStack accepts the key that the dev dump holds for the ALL runner.
	cfg, err := config.LoadFile(t.Context(), "../run-controller/runner-config.yml")
	require.NoError(t, err)
	publicKey, err := cfg.Crypto.FirstPublicKey()
	require.NoError(t, err)

	created, err := s.api.BuildingBlockRunner.Create(t.Context(), client.MeshBuildingBlockRunner{
		Metadata: client.MeshBuildingBlockRunnerMetadata{OwnedByWorkspace: workspace},
		Spec: client.MeshBuildingBlockRunnerSpec{
			DisplayName:        "building-block-runner TestAcc",
			PublicKey:          publicKey,
			ImplementationType: string(client.MeshBuildingBlockRunnerImplementationTypeAll),
			Restriction:        new("PRIVATE"),
		},
	})
	require.NoError(t, err)
	runnerUuid := *created.Metadata.Uuid
	t.Cleanup(func() {
		assert.NoError(t, s.api.BuildingBlockRunner.Delete(context.Background(), runnerUuid))
	})

	cfg.Uuid = uuid.MustParse(runnerUuid)
	cfg.Api.Url = xurl.MustParse(s.endpoint)
	cfg.Api.Auth.Workspace = workspace
	cfg.Api.Auth.Key.ClientId, cfg.Api.Auth.Key.ClientSecret = s.runnerKey, s.runnerSecret
	return cfg
}

func registrationUpdatesTheRunner(s localStack, cfg config.Config) func(*testing.T) {
	return func(t *testing.T) {
		t.Helper()
		cfg.Dispatcher.Register = &config.Register{DisplayName: "building-block-runner TestAcc, registered"}
		require.NoError(t, dispatcher.Register(t.Context(), cfg))

		after, err := s.api.BuildingBlockRunner.Read(t.Context(), cfg.Uuid.String())
		require.NoError(t, err)
		assert.Equal(t, "building-block-runner TestAcc, registered", after.Spec.DisplayName)
		assert.Equal(t, string(client.MeshBuildingBlockRunnerImplementationTypeAll), after.Spec.ImplementationType)
	}
}

func claimWithoutWorkFindsNoRun(s localStack, cfg config.Config) func(*testing.T) {
	return func(t *testing.T) {
		t.Helper()
		_, err := s.api.Runner.ClaimRun(t.Context(), cfg.Uuid)

		require.ErrorIs(t, err, client.ErrNoRunToClaim)

		runner, err := s.api.BuildingBlockRunner.Read(t.Context(), cfg.Uuid.String())
		require.NoError(t, err)
		assert.NotNil(t, runner.Metadata.LastSeen, "meshStack shows a runner that claims as online through lastSeen")
	}
}
