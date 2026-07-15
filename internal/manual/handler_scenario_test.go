package manual

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/meshapitest"
)

const testUuid = "d943b032-7836-4fef-a4a0-158817beecf3"

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// buildRun assembles a ClaimedRun the way the runner type would: Details parsed + RawJson =
// base64 of the same bytes (so decodeInputs re-reads with UseNumber for fidelity).
func buildRun(t *testing.T, token string, inputs []meshapi.BuildingBlockInputSpecDTO) dispatch.ClaimedRun {
	t.Helper()
	dto := &meshapi.RunDetailsDTO{
		Metadata: meshapi.RunMetaDTO{Uuid: testUuid},
		Spec: meshapi.RunSpecDTO{
			RunToken:      token,
			BuildingBlock: meshapi.BuildingBlockSpecDTO{Spec: meshapi.BuildingBlockDetailsSpecDTO{Inputs: inputs}},
		},
	}
	raw, err := json.Marshal(dto)
	require.NoError(t, err)
	return dispatch.ClaimedRun{
		Id:      dispatch.RunId(testUuid),
		Type:    meshapi.RunnerTypeManual,
		Details: dto,
		RawJson: base64.StdEncoding.EncodeToString(raw),
	}
}

func factoryFor(srv *meshapitest.Server) ReporterFactory {
	return NewReporterFactory(srv.URL, testUuid, meshapi.Identity{Name: "manual-block-runner", Version: "test"}, testLog())
}

func decodePatch(t *testing.T, body []byte) meshapi.SourceUpdateDTO {
	t.Helper()
	var u meshapi.SourceUpdateDTO
	require.NoError(t, json.Unmarshal(body, &u))
	return u
}

// Scenario_Manual_PollingRun_EchoesInputsAndSucceeds pins the whole production path:
// exactly one register (one PENDING "manual" step) then one PATCH (run SUCCEEDED, step
// "manual" SUCCEEDED, outputs echoed), and covers sensitive ciphertext echo, duplicate key
// last-wins, large-number fidelity, the type mapping, sensitivity preservation, and
// register-before-update ordering/cardinality.
func TestScenario_Manual_PollingRun_EchoesInputsAndSucceeds(t *testing.T) {
	srv := meshapitest.NewServer(t)

	inputs := []meshapi.BuildingBlockInputSpecDTO{
		{Key: "secret", Value: "ENC(opaque-ciphertext)", Type: typeString, IsSensitive: true},
		{Key: "dup", Value: "first", Type: typeString},
		{Key: "dup", Value: "last", Type: typeString}, // last wins
		{Key: "big", Value: json.Number("123456789012345678901234"), Type: typeInteger},
		{Key: "afile", Value: "data", Type: typeFile},  // FILE -> STRING
		{Key: "alist", Value: "[1,2]", Type: typeList}, // LIST -> CODE
	}
	run := buildRun(t, "run-token-xyz", inputs)

	h := NewHandler(Config{Uuid: testUuid}, HandlerDeps{Reporters: factoryFor(srv), Log: testLog()})
	require.NoError(t, h.Execute(context.Background(), run))

	// exactly one register then exactly one update.
	regs := srv.Registers()
	require.Len(t, regs, 1)
	require.Equal(t, testUuid, regs[0].Registration.Source.Id)
	require.Len(t, regs[0].Registration.Steps, 1)
	require.Equal(t, StepId, regs[0].Registration.Steps[0].Id)
	require.Equal(t, StepDisplayName, regs[0].Registration.Steps[0].DisplayName)
	require.NotNil(t, regs[0].Registration.Steps[0].Status)
	require.Equal(t, "PENDING", *regs[0].Registration.Steps[0].Status)

	patches := srv.Patches()
	require.Len(t, patches, 1)
	require.Equal(t, testUuid, patches[0].SourceId) // {sourceId} substituted with runner uuid
	// Run-scoped auth is the run's own token (runToken-only).
	require.Equal(t, "Bearer run-token-xyz", patches[0].Header.Get("Authorization"))

	upd := decodePatch(t, patches[0].Body)
	require.Equal(t, "SUCCEEDED", upd.Status)
	require.Len(t, upd.Steps, 1)
	require.Equal(t, StepId, upd.Steps[0].Id)
	require.Equal(t, "SUCCEEDED", upd.Steps[0].Status)

	out := upd.Steps[0].Outputs
	require.Equal(t, "ENC(opaque-ciphertext)", out["secret"].Value) // echoed verbatim
	require.True(t, out["secret"].Sensitive)                        // sensitivity preserved
	require.Equal(t, "last", out["dup"].Value)
	require.Equal(t, typeInteger, out["big"].Type)
	require.Equal(t, typeString, out["afile"].Type) // FILE -> STRING
	require.Equal(t, typeCode, out["alist"].Type)   // LIST -> CODE

	// the large integer round-trips byte-faithfully ON THE WIRE (json.Number, not
	// float64). We inspect the raw captured body — decoding it back through default
	// encoding/json would itself float64-ize it, hiding the fidelity we are proving.
	require.Contains(t, string(patches[0].Body), "123456789012345678901234")
}

// TestScenario_Manual_UnknownInputType echoes an unknown type unchanged (identity) rather
// than failing the run.
func TestScenario_Manual_UnknownInputType(t *testing.T) {
	srv := meshapitest.NewServer(t)
	run := buildRun(t, "tok", []meshapi.BuildingBlockInputSpecDTO{{Key: "k", Value: "v", Type: "WEIRD"}})
	h := NewHandler(Config{Uuid: testUuid}, HandlerDeps{Reporters: factoryFor(srv), Log: testLog()})
	require.NoError(t, h.Execute(context.Background(), run))
	upd := decodePatch(t, srv.Patches()[0].Body)
	require.Equal(t, "WEIRD", upd.Steps[0].Outputs["k"].Type)
}

// TestScenario_Manual_ReportFailureReturnsError pins the RunHandler contract: a status-PATCH
// transport failure surfaces as a non-nil error (the run stays unreported). This is
// the twin driving the single-run exit-1 path.
func TestScenario_Manual_ReportFailureReturnsError(t *testing.T) {
	srv := meshapitest.NewServer(t)
	srv.SeedPatchResponse(meshapitest.PatchResponse{Status: 500})
	run := buildRun(t, "tok", nil)
	h := NewHandler(Config{Uuid: testUuid}, HandlerDeps{Reporters: factoryFor(srv), Log: testLog()})
	require.Error(t, h.Execute(context.Background(), run))
}

// TestScenario_Manual_RegisterConflictTolerated: a 409-on-register is success.
func TestScenario_Manual_RegisterConflictTolerated(t *testing.T) {
	srv := meshapitest.NewServer(t)
	srv.SeedRegisterResponse(409)
	run := buildRun(t, "tok", nil)
	h := NewHandler(Config{Uuid: testUuid}, HandlerDeps{Reporters: factoryFor(srv), Log: testLog()})
	require.NoError(t, h.Execute(context.Background(), run))
	require.Len(t, srv.Patches(), 1)
}

// TestScenario_Manual_BackendAbort pins T1 for manual: the run has no poll loop, so it acts
// on a backend-requested abort by replacing the otherwise-due terminal update with a single
// ABORTED one — never reporting SUCCEEDED as the run's final state, and never erroring.
func TestScenario_Manual_BackendAbort(t *testing.T) {
	srv := meshapitest.NewServer(t)
	srv.SeedPatchResponse(meshapitest.PatchResponse{Status: 200, Abort: true})
	run := buildRun(t, "tok", nil)
	h := NewHandler(Config{Uuid: testUuid}, HandlerDeps{Reporters: factoryFor(srv), Log: testLog()})
	require.NoError(t, h.Execute(context.Background(), run))

	patches := srv.Patches()
	require.Len(t, patches, 2, "the would-be SUCCEEDED PATCH, then the ABORTED follow-up")
	first := decodePatch(t, patches[0].Body)
	require.Equal(t, "SUCCEEDED", first.Status, "the run still sends its normal terminal update")

	last := decodePatch(t, patches[len(patches)-1].Body)
	require.Equal(t, "ABORTED", last.Status, "but the FINAL reported state is ABORTED, not SUCCEEDED")
	require.Len(t, last.Steps, 1)
	require.Equal(t, StepId, last.Steps[0].Id)
	require.Equal(t, "ABORTED", last.Steps[0].Status)
}

// TestScenario_Manual_DebugMode_BackendAbort covers the executeDebug abort branch: an abort
// signalled on the first IN_PROGRESS PATCH stops the update sequence immediately — no
// further waits, no more IN_PROGRESS/final updates — and reports terminal ABORTED.
func TestScenario_Manual_DebugMode_BackendAbort(t *testing.T) {
	srv := meshapitest.NewServer(t)
	srv.SeedPatchResponse(meshapitest.PatchResponse{Status: 200, Abort: true})
	clock := &fakeClock{}
	run := buildRun(t, "tok", nil)
	h := NewHandler(Config{Uuid: testUuid, DebugMode: true}, HandlerDeps{
		Reporters: factoryFor(srv),
		Clock:     clock,
		Log:       testLog(),
	})
	require.NoError(t, h.Execute(context.Background(), run))
	require.Equal(t, 0, clock.waits, "the sequence stops before any Clock.Wait")

	patches := srv.Patches()
	require.Len(t, patches, 2, "the would-be IN_PROGRESS PATCH, then the ABORTED follow-up")
	last := decodePatch(t, patches[len(patches)-1].Body)
	require.Equal(t, "ABORTED", last.Status)
	require.Len(t, last.Steps, 1)
	require.Equal(t, "ABORTED", last.Steps[0].Status)
}

// fakeClock records debug-mode waits and returns immediately so tests are instant.
type fakeClock struct{ waits int }

func (c *fakeClock) Wait(context.Context, time.Duration) { c.waits++ }

// TestScenario_Manual_DebugMode: with debugMode the handler sends exactly 4
// updates (IN_PROGRESS×3 then terminal), each with the "manual" (SUCCEEDED, fixed messages)
// and "additionalDebugStep" steps; outputs appear only on the final update and carry the
// RAW input type (the toOutputType-skip quirk). Both RNG branches are exercised.
func TestScenario_Manual_DebugMode(t *testing.T) {
	cases := []struct {
		name      string
		rand      float64
		wantFinal string
	}{
		{"success branch", 0.4, "SUCCEEDED"},
		{"failure branch", 0.9, "FAILED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := meshapitest.NewServer(t)
			clock := &fakeClock{}
			run := buildRun(t, "tok", []meshapi.BuildingBlockInputSpecDTO{{Key: "k", Value: "v", Type: typeList}})
			h := NewHandler(Config{Uuid: testUuid, DebugMode: true}, HandlerDeps{
				Reporters: factoryFor(srv),
				Clock:     clock,
				Rand:      func() float64 { return tc.rand },
				Log:       testLog(),
			})
			require.NoError(t, h.Execute(context.Background(), run))
			require.Equal(t, 3, clock.waits, "three inter-update waits")

			patches := srv.Patches()
			require.Len(t, patches, 4)
			for i, p := range patches {
				upd := decodePatch(t, p.Body)
				require.Len(t, upd.Steps, 2, "every debug update carries two steps")
				require.Equal(t, StepId, upd.Steps[0].Id)
				require.Equal(t, "SUCCEEDED", upd.Steps[0].Status)
				require.Equal(t, debugUserMessage, upd.Steps[0].UserMessage)
				require.Equal(t, debugSystemMessage, upd.Steps[0].SystemMessage)
				require.Equal(t, debugStepId, upd.Steps[1].Id)
				if i < 3 {
					require.Equal(t, "IN_PROGRESS", upd.Status)
					require.Equal(t, "PENDING", upd.Steps[1].Status)
					require.Empty(t, upd.Steps[1].Outputs, "no outputs on non-final debug updates")
				} else {
					require.Equal(t, tc.wantFinal, upd.Status)
					require.Equal(t, "SUCCEEDED", upd.Steps[1].Status)
					// Debug outputs echo the RAW input type (LIST, not CODE) — the quirk.
					require.Equal(t, typeList, upd.Steps[1].Outputs["k"].Type)
				}
			}
		})
	}
}
