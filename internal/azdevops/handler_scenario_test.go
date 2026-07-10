package azdevops

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/meshapitest"
)

// Test_Scenario_AsyncHandover pins A-P1/A-P2/U-P1 (async variant): register-before-
// everything, exactly one trigger-success update (IN_PROGRESS handover, D9), zero poll GETs,
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

// Test_Scenario_RegisterFailurePropagates pins A-P2: a register transport failure returns a
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

// Test_Scenario_TriggerFailures is A-P3/U-P8: the failure ladder after register, each rung
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

	t.Run("PAT decrypt failure", func(t *testing.T) {
		ado := newSeqADO(t)
		srv := meshapitest.NewServer(t)
		run := buildRun(t, ado.URL, "tok", implFixture{PersonalAccessToken: "pat"}, nil)

		h := NewHandler(Config{Uuid: testUuid}, HandlerDeps{
			Reporters: factoryFor(srv),
			Decryptor: failingDecryptor{},
			HTTP:      NewHTTPClient(0),
			Clock:     newFakeClock(),
			Log:       testLog(),
		})
		require.NoError(t, execute(t, h, run))

		upd := decodePatch(t, srv.Patches()[0].Body)
		require.Equal(t, "FAILED", upd.Status)
		require.Equal(t, "Could not trigger the Azure DevOps Pipeline", upd.Steps[0].UserMessage)
		require.Contains(t, upd.Steps[0].SystemMessage, "internal error")
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

// failingDecryptor always fails -- used to drive the PAT/input decrypt-failure rungs.
type failingDecryptor struct{}

func (failingDecryptor) Decrypt(string) (string, error) { return "", errDecryptTest }

var errDecryptTest = &decryptTestError{}

type decryptTestError struct{}

func (*decryptTestError) Error() string { return "decrypt failed (test)" }

// Test_Scenario_TriggerPayload_InputsAndBehavior is A-P4: non-env inputs stringified via
// renderValue, environment inputs excluded entirely, MESHSTACK_BEHAVIOR present and
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

// Test_Scenario_SensitiveInputsDecrypted is F-P1: sensitive STRING decrypted into
// templateParameters, sensitive LIST left encrypted (the Kotlin asymmetry quirk); the PAT
// itself never appears in the payload (§7.6 leak pin, cross-checked at the handler level).
func Test_Scenario_SensitiveInputsDecrypted(t *testing.T) {
	ado := newSeqADO(t)
	srv := meshapitest.NewServer(t)

	crypto := testCryptoForHandler(t)
	stringCipher := encryptForTestHandler(t, crypto, "plain-secret")
	listCipher := encryptForTestHandler(t, crypto, "list-secret")

	inputs := []meshapi.BuildingBlockInputSpecDTO{
		{Key: "secret", Value: stringCipher, Type: "STRING", IsSensitive: true},
		{Key: "listy", Value: listCipher, Type: "LIST", IsSensitive: true},
	}
	patCipher := encryptForTestHandler(t, crypto, "the-real-pat")
	run := buildRun(t, ado.URL, "tok", implFixture{PersonalAccessToken: patCipher, Async: true}, inputs)

	dec, err := meshapi.NewCertDecryptor(mustReadTestKeyHandler(t))
	require.NoError(t, err)
	h := NewHandler(Config{Uuid: testUuid}, HandlerDeps{
		Reporters: factoryFor(srv),
		Decryptor: dec,
		HTTP:      NewHTTPClient(0),
		Clock:     newFakeClock(),
		Log:       testLog(),
	})
	require.NoError(t, execute(t, h, run))

	reqs := ado.Requests()
	require.Len(t, reqs, 1)
	require.NotContains(t, string(reqs[0].Body), "the-real-pat", "the PAT must never appear in the payload")

	var payload map[string]any
	require.NoError(t, json.Unmarshal(reqs[0].Body, &payload))
	params := asMap(t, payload["templateParameters"])
	require.Equal(t, "plain-secret", params["secret"])
	require.Equal(t, listCipher, params["listy"], "sensitive LIST stays ciphertext (F-P1 quirk)")

	// The auth header carries the decrypted PAT (correctly, since it's used for auth, not payload).
	wantAuth := "Basic " + basicAuth("the-real-pat")
	require.Equal(t, wantAuth, reqs[0].Header.Get("Authorization"))
}

func basicAuth(pat string) string {
	c := adoClient{pat: pat}
	req := &http.Request{Header: http.Header{}}
	c.setAuthHeader(req)
	return req.Header.Get("Authorization")[len("Basic "):]
}
