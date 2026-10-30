package testacc

import (
	"slices"
	"strings"
	"testing"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/v2/internal/testacc/harness"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/keycloak"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/runnerproc"
)

func TestAccWif(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)
	kc := keycloak.New(t, s.Keep)
	wifCase := func(t *testing.T, runner runnerproc.Options, purgeOnDelete bool) harness.Case {
		t.Helper()
		return harness.Case{Files: caseFiles(t), ImplementationType: "TERRAFORM", Runner: runner, Vars: map[string]any{"purge_on_delete": purgeOnDelete}}
	}

	t.Run("Keycloak", func(t *testing.T) {
		t.Parallel()
		runner := runnerproc.Options{ForType: "tf", RegisterAs: "building-block-runner TestAcc, Keycloak WIF", KeycloakWif: kc.RunnerConfig(t, kc.ClientSecret)}

		d := harness.Deploy(t, s, wifCase(t, runner, false))

		runLog := d.RequireStatus(t, client.BuildingBlockStatusSucceeded)
		assert.Contains(t, runLog, "The pre-run script found the token that the tf var names.")
		claims := keycloak.DecodeClaims(t, d.Output[string](t, "wif_token"))
		assert.Equal(t, kc.Issuer, claims["iss"])
		assert.Equal(t, "bbd:"+d.Name+":"+d.Bootstrap.Output[string](t, "definition_uuid"), claims["sub"])
		assert.Equal(t, "meshstack-wif", claims["aud"])
		assert.Empty(t, kc.RunClientIds(t), "a per-run client outlived its token")

		registered, err := s.Api.BuildingBlockRunner.Read(t.Context(), d.Runner.Uuid)
		require.NoError(t, err)
		wif := registered.Spec.WorkloadIdentityFederation
		require.NotNil(t, wif, "meshStack shows no workload identity of the runner")
		assert.Equal(t, kc.Issuer, *wif.Issuer)
		assert.Equal(t, "bbd:{{ workspaceIdentifier }}:{{ buildingBlockDefinitionUuid }}", *wif.SubjectTemplate)
		for _, provider := range []*client.MeshRunnerWifProviderConfig{wif.Aws, wif.Azure, wif.Gcp} {
			assert.Equal(t, &client.MeshRunnerWifProviderConfig{Audience: "meshstack-wif", TokenPath: ".meshstack/wif-token"}, provider)
		}
	})

	t.Run("AdminLoginFails", func(t *testing.T) {
		t.Parallel()
		runner := runnerproc.Options{ForType: "tf", KeycloakWif: kc.RunnerConfig(t, "wrong-secret")}

		// The destroy run fails to get its token too.
		d := harness.Deploy(t, s, wifCase(t, runner, true))

		runLog := d.RequireStatus(t, client.BuildingBlockStatusFailed)
		assert.Contains(t, runLog, "meshStack could not issue the workload identity token for this run.")
		records := d.Runner.Records.OfRun(t, d.LatestRunUuid(t))
		runnerproc.AssertRecord(t, records, "of the runner that logs the cause", func(r runnerproc.Record) bool {
			return r.Attr("msg") == "issuing the workload identity token failed" && strings.Contains(r.Attr("error"), "admin login")
		})
		assert.False(t, slices.ContainsFunc(records, func(r runnerproc.Record) bool { return r.Attr("msg") == "executing the tf run" }), "a run process started")
	})
}
