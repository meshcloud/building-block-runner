package testacc

import (
	"context"
	"log"
	"testing"
	"uuid"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/config/xurl"
	"github.com/meshcloud/building-block-runner/internal/dispatcher"
	internalhttp "github.com/meshcloud/building-block-runner/internal/http"
	"github.com/meshcloud/building-block-runner/internal/http/meshapi"
)

var userAgent = internalhttp.UserAgent{GitHubRepo: "meshcloud/building-block-runner", Version: "0.0.0-testacc"}

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
		{"run-controller's registration updates the runner", registrationUpdatesTheRunner(stack, runner)},
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
	cfg, err := config.LoadFile("../run-controller/runner-config.yml", log.New(t.Output(), "", 0))
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
		cfg.Dispatcher.Register = &config.Register{DisplayName: "building-block-runner TestAcc, registered"}
		require.NoError(t, dispatcher.Register(t.Context(), cfg, log.New(t.Output(), "", 0), userAgent))

		after, err := s.api.BuildingBlockRunner.Read(t.Context(), cfg.Uuid.String())
		require.NoError(t, err)
		assert.Equal(t, "building-block-runner TestAcc, registered", after.Spec.DisplayName)
		assert.Equal(t, string(client.MeshBuildingBlockRunnerImplementationTypeAll), after.Spec.ImplementationType)
	}
}

func claimWithoutWorkFindsNoRun(s localStack, cfg config.Config) func(*testing.T) {
	return func(t *testing.T) {
		client := internalhttp.NewClient(userAgent)
		runnerAuth := internalhttp.NewApiKeyLogin(client, cfg.Api.Url.URL, s.runnerKey, s.runnerSecret)
		_, err := meshapi.NewMeshRunnerClient(client.WithAuthorization(runnerAuth), cfg.Api.Url.URL).ClaimRun(t.Context(), cfg.Uuid)

		require.ErrorIs(t, err, meshapi.ErrNoRunToClaim)

		runner, err := s.api.BuildingBlockRunner.Read(t.Context(), cfg.Uuid.String())
		require.NoError(t, err)
		assert.NotNil(t, runner.Metadata.LastSeen, "meshStack shows a runner that claims as online through lastSeen")
	}
}
