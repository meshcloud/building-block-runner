package testacc

import (
	"slices"
	"strings"
	"testing"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const wifTokenFailure = "meshStack could not issue the workload identity token for this run."

func wifCase(runner runnerOptions, purgeOnDelete bool) testCase {
	return testCase{name: "wif", implementationType: "TERRAFORM", runner: runner, vars: map[string]any{"purge_on_delete": purgeOnDelete}}
}

func TestAccKeycloakWif(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)
	runner := runnerOptions{forType: "tf", registerAs: "building-block-runner TestAcc, Keycloak WIF", keycloakWif: s.keycloak.runnerConfig(s.keycloak.clientSecret)}

	d := s.deploy(t, wifCase(runner, false))

	bb := d.readBuildingBlock(t)
	runLog := d.runLog(t, d.latestRunUuid(t))
	require.Equalf(t, client.BuildingBlockStatusSucceeded, bb.Status.Status, "run log:\n%s", runLog)
	assert.Contains(t, runLog, "The pre-run script found the token that the tf var names.")
	token, ok := outputValues(bb)["wif_token"].(string)
	require.True(t, ok, "the building block has no wif_token output")
	claims := decodeClaims(t, token)
	assert.Equal(t, s.keycloak.issuer(), claims["iss"])
	assert.Equal(t, "bbd:"+d.name+":"+d.bootstrap.output(t, "definition_uuid"), claims["sub"])
	assert.Equal(t, "meshstack-wif", claims["aud"])
	assert.Empty(t, s.keycloak.runClientIds(t), "a per-run client outlived its token")

	registered, err := s.api.BuildingBlockRunner.Read(t.Context(), d.runner.uuid)
	require.NoError(t, err)
	wif := registered.Spec.WorkloadIdentityFederation
	require.NotNil(t, wif, "meshStack shows no workload identity of the runner")
	assert.Equal(t, s.keycloak.issuer(), *wif.Issuer)
	assert.Equal(t, "bbd:{{ workspaceIdentifier }}:{{ buildingBlockDefinitionUuid }}", *wif.SubjectTemplate)
	for _, provider := range []*client.MeshRunnerWifProviderConfig{wif.Aws, wif.Azure, wif.Gcp} {
		assert.Equal(t, &client.MeshRunnerWifProviderConfig{Audience: "meshstack-wif", TokenPath: ".meshstack/wif-token"}, provider)
	}
}

func TestAccKeycloakWifAdminLoginFails(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)
	runner := runnerOptions{forType: "tf", keycloakWif: s.keycloak.runnerConfig("wrong-secret")}

	// The destroy run fails to get its token too.
	d := s.deploy(t, wifCase(runner, true))

	bb := d.readBuildingBlock(t)
	runUuid := d.latestRunUuid(t)
	runLog := d.runLog(t, runUuid)
	require.Equalf(t, client.BuildingBlockStatusFailed, bb.Status.Status, "run log:\n%s", runLog)
	assert.Contains(t, runLog, wifTokenFailure)
	records := d.runner.records.ofRun(runUuid)
	assertRecord(t, records, "of the runner that logs the cause", func(r record) bool {
		return r.attr("msg") == "issuing the workload identity token failed" && strings.Contains(r.attr("error"), "admin login")
	})
	assert.False(t, slices.ContainsFunc(records, func(r record) bool { return r.attr("msg") == "executing the tf run" }), "a run process started")
}
