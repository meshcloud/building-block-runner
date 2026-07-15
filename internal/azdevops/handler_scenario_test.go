package azdevops

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/meshapitest"
)

// Test_Scenario_AsyncHandover pins the async variant: register-before-
// everything, exactly one trigger-success update (IN_PROGRESS handover), zero poll GETs,
// and the async message suffix ("Will wait for API updates on status...").
func Test_Scenario_AsyncHandover(t *testing.T) {
	ado := newSeqADO(t)
	srv := meshapitest.NewServer(t)

	run := buildRun(t, ado.URL, "run-token-xyz", implFixture{PersonalAccessToken: "pat", Async: true}, nil)
	h := newTestHandler(factoryFor(srv), newFakeClock())

	require.NoError(t, execute(t, h, run))

	regs := srv.Registers()
	require.Len(t, regs, 1, "register happens exactly once, before anything else (A-P2)")
	require.Equal(t, StepId, regs[0].Registration.Steps[0].Id)

	patches := srv.Patches()
	require.Len(t, patches, 1, "async handover is exactly one update")
	require.Equal(t, testUuid, patches[0].SourceId)
	require.Equal(t, "Bearer run-token-xyz", patches[0].Header.Get("Authorization"), "runToken-only auth (risk #5)")

	upd := decodePatch(t, patches[0].Body)
	require.Equal(t, "IN_PROGRESS", upd.Status, "async handover reports IN_PROGRESS, D9")
	require.Len(t, upd.Steps, 1)
	require.Equal(t, "SUCCEEDED", upd.Steps[0].Status)
	require.Contains(t, upd.Steps[0].UserMessage, "Will wait for API updates on status...")
	require.Contains(t, upd.Steps[0].SystemMessage, "Triggered pipeline run 1")

	adoReqs := ado.Requests()
	require.Len(t, adoReqs, 1, "exactly one ADO call: the trigger POST; zero poll GETs")
}

// Test_Scenario_RegisterFailurePropagates pins that a register transport failure returns a
// non-nil error and no FAILED update is ever sent.
func Test_Scenario_RegisterFailurePropagates(t *testing.T) {
	ado := newSeqADO(t)
	srv := meshapitest.NewServer(t)
	srv.SeedRegisterResponse(500)

	run := buildRun(t, ado.URL, "tok", implFixture{PersonalAccessToken: "pat", Async: true}, nil)
	h := newTestHandler(factoryFor(srv), newFakeClock())

	require.Error(t, execute(t, h, run))
	require.Empty(t, srv.Patches(), "no FAILED update when register itself failed")
}

// Test_Scenario_TriggerFailures covers the failure ladder after register, each rung
// reporting run FAILED + step FAILED with the pinned message pair.
func Test_Scenario_TriggerFailures(t *testing.T) {
	t.Run("wrong implementation type", func(t *testing.T) {
		ado := newSeqADO(t)
		srv := meshapitest.NewServer(t)
		run := buildRun(t, ado.URL, "tok", implFixture{PersonalAccessToken: "pat"}, nil)
		// Corrupt the implementation type discriminator post-hoc (defense-in-depth path,
		// structurally unreachable via normal dispatch -- see parseImplementation's doc).
		var raw map[string]any
		require.NoError(t, json.Unmarshal(run.Details.Spec.Definition.Spec.Implementation, &raw))
		raw["type"] = "TERRAFORM"
		newImpl, err := json.Marshal(raw)
		require.NoError(t, err)
		run.Details.Spec.Definition.Spec.Implementation = newImpl

		h := newTestHandler(factoryFor(srv), newFakeClock())
		require.NoError(t, execute(t, h, run))

		upd := decodePatch(t, srv.Patches()[0].Body)
		require.Equal(t, "FAILED", upd.Status)
		require.Equal(t, "Could not trigger the Azure DevOps Pipeline", upd.Steps[0].UserMessage)
		require.Contains(t, upd.Steps[0].SystemMessage, "internal error")
		require.Contains(t, upd.Steps[0].SystemMessage, "was not of expected type")
	})

	t.Run("trigger 404", func(t *testing.T) {
		ado := newSeqADO(t)
		ado.triggerResp = adoResp{status: 404, body: `{"message":"no such pipeline"}`}
		srv := meshapitest.NewServer(t)
		run := buildRun(t, ado.URL, "tok", implFixture{PersonalAccessToken: "pat"}, nil)

		h := newTestHandler(factoryFor(srv), newFakeClock())
		require.NoError(t, execute(t, h, run))

		upd := decodePatch(t, srv.Patches()[0].Body)
		require.Equal(t, "FAILED", upd.Status)
		require.Equal(t, "Could not trigger the Azure DevOps Pipeline", upd.Steps[0].UserMessage)
		require.Contains(t, upd.Steps[0].SystemMessage, "Request:")
		require.Contains(t, upd.Steps[0].SystemMessage, "responded with status: 404")
		require.Contains(t, upd.Steps[0].SystemMessage, "no such pipeline")
	})
}

// Test_Scenario_TriggerPayload_InputsAndBehavior covers non-env inputs stringified via
// valuestring.Render, environment inputs excluded entirely, MESHSTACK_BEHAVIOR present and
// overwriting a same-keyed user input.
func Test_Scenario_TriggerPayload_InputsAndBehavior(t *testing.T) {
	ado := newSeqADO(t)
	srv := meshapitest.NewServer(t)

	inputs := []meshapi.BuildingBlockInputSpecDTO{
		{Key: "name", Value: "hello", Type: "STRING"},
		{Key: "count", Value: json.Number("123456789012345678901234"), Type: "INTEGER"},
		{Key: "MESHSTACK_BEHAVIOR", Value: "user-supplied-should-be-overwritten", Type: "STRING"},
		{Key: "envvar", Value: "excluded", Type: "STRING", Env: true},
	}
	run := buildRun(t, ado.URL, "tok", implFixture{PersonalAccessToken: "pat", Async: true}, inputs)
	h := newTestHandler(factoryFor(srv), newFakeClock())
	require.NoError(t, execute(t, h, run))

	reqs := ado.Requests()
	require.Len(t, reqs, 1)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(reqs[0].Body, &payload))
	params := asMap(t, payload["templateParameters"])

	require.Equal(t, "hello", params["name"])
	require.Equal(t, "123456789012345678901234", params["count"], "large integer preserved via UseNumber, not float64-ized")
	require.Equal(t, "APPLY", params["MESHSTACK_BEHAVIOR"], "overwrites the same-keyed user input")
	_, hasEnvVar := params["envvar"]
	require.False(t, hasEnvVar, "environment inputs are excluded entirely")
}

// Test_Scenario_SensitiveInputsForwardedPlaintext covers the post-boundary-decryption
// shape: inputs arrive already-plaintext (no Decryptor in this handler any more), sensitive
// ones are forwarded into templateParameters unchanged, and exactly one WARN is logged
// naming the forwarded sensitive keys (meshapi.SensitiveInputKeys).
func Test_Scenario_SensitiveInputsForwardedPlaintext(t *testing.T) {
	ado := newSeqADO(t)
	srv := meshapitest.NewServer(t)

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	h := NewHandler(Config{Uuid: testUuid}, HandlerDeps{
		Reporters: factoryFor(srv),
		HTTP:      NewHTTPClient(0),
		Clock:     newFakeClock(),
		Log:       log,
	})

	inputs := []meshapi.BuildingBlockInputSpecDTO{
		{Key: "secret", Value: "plain-secret", Type: "STRING", IsSensitive: true},
	}
	run := buildRun(t, ado.URL, "tok", implFixture{PersonalAccessToken: "the-real-pat", Async: true}, inputs)

	require.NoError(t, execute(t, h, run))

	reqs := ado.Requests()
	require.Len(t, reqs, 1)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(reqs[0].Body, &payload))
	params := asMap(t, payload["templateParameters"])
	require.Equal(t, "plain-secret", params["secret"], "sensitive inputs are forwarded as-is (decrypted at the dispatch boundary)")

	wantAuth := "Basic " + basicAuth("the-real-pat")
	require.Equal(t, wantAuth, reqs[0].Header.Get("Authorization"))

	logged := buf.String()
	require.Contains(t, logged, "forwarding sensitive inputs as Azure DevOps template parameters")
	require.Contains(t, logged, "sensitiveInputKeys")
	require.Contains(t, logged, "secret")
}

func basicAuth(pat string) string {
	return base64.StdEncoding.EncodeToString([]byte(":" + pat))
}
