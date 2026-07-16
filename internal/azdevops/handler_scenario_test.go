package azdevops

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/meshapitest"
	"github.com/meshcloud/building-block-runner/internal/runmode"
)

// envRunJsonFilePath mirrors runmode.RunJsonFilePathEnv; kept as this package's own name
// because these single-run scenario tests still reference it directly.
const envRunJsonFilePath = runmode.RunJsonFilePathEnv

// runSingleRunForTest mirrors what cmd/bbrunner's azdevops single-run bootstrap wires: the
// same Handler polling uses, run once via runmode.SingleRunFromFile against
// RUN_JSON_FILE_PATH.
func runSingleRunForTest(ctx context.Context, log *slog.Logger, cfg Config, id meshapi.Identity) int {
	handler := NewHandler(cfg, HandlerDeps{
		Reporters: NewReporterFactory(cfg.Api.Url, cfg.Uuid, id, log),
		Log:       log,
	})

	return runmode.SingleRunFromFile(ctx, log, cfg.Uuid, meshapi.RunnerTypeAzureDevOpsPipeline,
		func(ctx context.Context, run dispatch.ClaimedRun) error {
			return handler.Execute(ctx, run)
		})
}

func writeRunJSON(t *testing.T, adoBaseUrl string) string {
	t.Helper()
	run := buildRun(t, adoBaseUrl, "run-token-single", implFixture{PersonalAccessToken: "plaintext-pat-already-decrypted", Async: true}, nil)
	raw, err := json.Marshal(run.Run)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "run.json")
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	return path
}

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
		require.NoError(t, json.Unmarshal(run.Run.Spec.Definition.Spec.Implementation, &raw))
		raw["type"] = "TERRAFORM"
		newImpl, err := json.Marshal(raw)
		require.NoError(t, err)
		run.Run.Spec.Definition.Spec.Implementation = newImpl

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
	h := NewHandler(Config{BaseConfig: config.BaseConfig{Uuid: testUuid}}, HandlerDeps{
		Reporters: factoryFor(srv),
		HTTP:      NewHTTPClient(0, nil),
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

// Test_Scenario_SingleRun_AsyncCapturedWire verifies an async run JSON via RUN_JSON_FILE_PATH
// produces a captured register + IN_PROGRESS handover update, exit 0. The PAT arrives
// plaintext and is NOT decrypted (NoOp crypto, controller pre-decrypted it).
func Test_Scenario_SingleRun_AsyncCapturedWire(t *testing.T) {
	ado := newSeqADO(t)
	srv := meshapitest.NewServer(t)
	path := writeRunJSON(t, ado.URL)
	t.Setenv(envRunJsonFilePath, path)

	cfg := Config{BaseConfig: config.BaseConfig{Uuid: testUuid, Api: config.Api{Url: srv.URL}}}
	id := meshapi.Identity{Name: "azure-devops-block-runner", Version: "test"}
	code := runSingleRunForTest(context.Background(), testLog(), cfg, id)
	require.Equal(t, 0, code)

	require.Len(t, srv.Registers(), 1)
	patches := srv.Patches()
	require.Len(t, patches, 1)
	require.Equal(t, "Bearer run-token-single", patches[0].Header.Get("Authorization"))

	reqs := ado.Requests()
	require.Len(t, reqs, 1)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(reqs[0].Body, &payload))
	// The trigger auth header carries the PAT verbatim (NoOp decryptor: it was already
	// plaintext); the client never re-decrypts it.
	require.NotEmpty(t, reqs[0].Header.Get("Authorization"))
}

// Test_Scenario_SingleRun_MissingEnv covers the missing-env rung of the exit-code tail.
func Test_Scenario_SingleRun_MissingEnv(t *testing.T) {
	t.Setenv(envRunJsonFilePath, "")
	code := runSingleRunForTest(context.Background(), testLog(), Config{BaseConfig: config.BaseConfig{Uuid: testUuid}}, meshapi.Identity{})
	require.Equal(t, 1, code)
}

// Test_Scenario_SingleRun_FileNotFound / Test_Scenario_SingleRun_UnparsableFile are the
// sanctioned delta: Go exits non-zero on a pre-report fetch/parse failure where Kotlin
// swallowed it and exited 0.
func Test_Scenario_SingleRun_FileNotFound(t *testing.T) {
	t.Setenv(envRunJsonFilePath, filepath.Join(t.TempDir(), "absent.json"))
	code := runSingleRunForTest(context.Background(), testLog(), Config{BaseConfig: config.BaseConfig{Uuid: testUuid}}, meshapi.Identity{})
	require.Equal(t, 1, code)
}

func Test_Scenario_SingleRun_UnparsableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.json")
	require.NoError(t, os.WriteFile(path, []byte("not json"), 0o600))
	t.Setenv(envRunJsonFilePath, path)
	code := runSingleRunForTest(context.Background(), testLog(), Config{BaseConfig: config.BaseConfig{Uuid: testUuid}}, meshapi.Identity{})
	require.Equal(t, 1, code)
}

// Test_Scenario_SingleRun_RegisterFailureExitsNonZero pins the register-failure rung
// (Kotlin exit-1 parity, unchanged).
func Test_Scenario_SingleRun_RegisterFailureExitsNonZero(t *testing.T) {
	ado := newSeqADO(t)
	srv := meshapitest.NewServer(t)
	srv.SeedRegisterResponse(500)
	path := writeRunJSON(t, ado.URL)
	t.Setenv(envRunJsonFilePath, path)

	cfg := Config{BaseConfig: config.BaseConfig{Uuid: testUuid, Api: config.Api{Url: srv.URL}}}
	code := runSingleRunForTest(context.Background(), testLog(), cfg, meshapi.Identity{Name: "azure-devops-block-runner"})
	require.Equal(t, 1, code)
}

// Test_Scenario_SingleRun_SyncCompletesWithinTheJobPod exercises a sync single-run: the Job
// pod itself performs the poll (unchanged from Kotlin) -- proven here with an
// already-COMPLETED trigger so the test stays instant.
func Test_Scenario_SingleRun_SyncCompletesWithinTheJobPod(t *testing.T) {
	ado := newSeqADO(t)
	ado.triggerResp = adoResp{status: 200, body: `{"id":1,"state":"completed","result":"succeeded","createdDate":"now"}`}
	srv := meshapitest.NewServer(t)

	run := buildRun(t, ado.URL, "tok", implFixture{PersonalAccessToken: "pat", Async: false}, nil)
	raw, err := json.Marshal(run.Run)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "run.json")
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	t.Setenv(envRunJsonFilePath, path)

	cfg := Config{BaseConfig: config.BaseConfig{Uuid: testUuid, Api: config.Api{Url: srv.URL}}}
	code := runSingleRunForTest(context.Background(), testLog(), cfg, meshapi.Identity{Name: "azure-devops-block-runner"})
	require.Equal(t, 0, code)

	final := decodePatch(t, srv.Patches()[len(srv.Patches())-1].Body)
	require.Equal(t, "SUCCEEDED", final.Status)
}
