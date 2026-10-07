package testacc

import (
	"context"
	"errors"
	"log"
	"net/http"
	"testing"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
	"github.com/meshcloud/building-block-runner/run-controller/controller"
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
		{"run-controller's registration updates the runner", registrationUpdatesTheRunner(stack, runner)},
		{"a claim without work finds no run and marks the runner as seen", claimWithoutWorkFindsNoRun(stack, runner)},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.step) {
			t.FailNow()
		}
	}
}

func (s localStack) createRunner(t *testing.T) string {
	t.Helper()
	// The certificate the dev dump holds for the ALL runner, rather than an invented key meshStack
	// may reject.
	runControllerConfig, err := controller.ReadInYmlConfig("../run-controller/runner-config.yml")
	require.NoError(t, err)

	created, err := s.api.BuildingBlockRunner.Create(t.Context(), client.MeshBuildingBlockRunner{
		Metadata: client.MeshBuildingBlockRunnerMetadata{OwnedByWorkspace: workspace},
		Spec: client.MeshBuildingBlockRunnerSpec{
			DisplayName:        "building-block-runner TestAcc",
			PublicKey:          runControllerConfig.Crypto.PublicKey,
			ImplementationType: string(client.MeshBuildingBlockRunnerImplementationTypeAll),
			Restriction:        new("PRIVATE"),
		},
	})
	require.NoError(t, err)
	runnerUuid := *created.Metadata.Uuid
	t.Cleanup(func() {
		assert.NoError(t, s.api.BuildingBlockRunner.Delete(context.Background(), runnerUuid))
	})
	return runnerUuid
}

func registrationUpdatesTheRunner(s localStack, runnerUuid string) func(*testing.T) {
	return func(t *testing.T) {
		before, err := s.api.BuildingBlockRunner.Read(t.Context(), runnerUuid)
		require.NoError(t, err)

		previous := controller.AppConfig
		t.Cleanup(func() { controller.AppConfig = previous })
		controller.AppConfig = &controller.ControllerConfig{
			Uuid:             runnerUuid,
			OwnedByWorkspace: workspace,
			DisplayName:      "building-block-runner TestAcc, registered",
			Crypto:           controller.CryptoConfig{PublicKey: before.Spec.PublicKey},
			Api:              controller.ApiConfig{Url: s.endpoint, ClientId: s.runnerKey, ClientSecret: s.runnerSecret},
		}
		require.NoError(t, controller.RegisterController(log.New(t.Output(), "", 0)))

		after, err := s.api.BuildingBlockRunner.Read(t.Context(), runnerUuid)
		require.NoError(t, err)
		assert.Equal(t, "building-block-runner TestAcc, registered", after.Spec.DisplayName)
		assert.Equal(t, string(client.MeshBuildingBlockRunnerImplementationTypeAll), after.Spec.ImplementationType)
	}
}

func claimWithoutWorkFindsNoRun(s localStack, runnerUuid string) func(*testing.T) {
	return func(t *testing.T) {
		runnerAuth := meshapi.NewApiKeyAuth(s.endpoint, s.runnerKey, s.runnerSecret)
		_, _, err := meshapi.NewClient(s.endpoint, runnerUuid, runnerAuth).FetchRun(runnerUuid)

		statusErr, ok := errors.AsType[*meshapi.StatusError](err)
		require.Truef(t, ok, "the claim did not answer with a status: %v", err)
		assert.Equal(t, http.StatusNotFound, statusErr.Status)

		runner, err := s.api.BuildingBlockRunner.Read(t.Context(), runnerUuid)
		require.NoError(t, err)
		assert.NotNil(t, runner.Metadata.LastSeen, "meshStack shows a runner that claims as online through lastSeen")
	}
}
