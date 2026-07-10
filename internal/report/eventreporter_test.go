package report

import (
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/meshapitest"
)

func repLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newTestReporter(srv *meshapitest.Server, sourceId string) Reporter {
	rc := meshapi.NewRunClient(srv.URL, "node", meshapi.BearerTokenAuth{Token: "tok"})
	return NewReporter(rc, sourceId, repLog())
}

// TestEventReporter_Register pins C-P3/C-P6 wire shape: register the source id with one
// PENDING step, run id substituted into the URL path.
func TestEventReporter_Register(t *testing.T) {
	srv := meshapitest.NewServer(t)
	r := newTestReporter(srv, "src-uuid")

	err := r.Register(RunStatus{RunId: "run-1", Steps: []StepStatus{{Name: "manual", DisplayName: "Manual Block Run"}}})
	require.NoError(t, err)

	regs := srv.Registers()
	require.Len(t, regs, 1)
	require.Equal(t, "run-1", regs[0].RunId)
	require.Equal(t, "src-uuid", regs[0].Registration.Source.Id)
	require.Len(t, regs[0].Registration.Steps, 1)
	require.Equal(t, "manual", regs[0].Registration.Steps[0].Id)
	require.NotNil(t, regs[0].Registration.Steps[0].Status)
	require.Equal(t, "PENDING", *regs[0].Registration.Steps[0].Status)
}

// TestEventReporter_RegisterConflictTolerated pins C-P4: 409 on register = success.
func TestEventReporter_RegisterConflictTolerated(t *testing.T) {
	srv := meshapitest.NewServer(t)
	srv.SeedRegisterResponse(409)
	require.NoError(t, newTestReporter(srv, "src").Register(RunStatus{RunId: "r", Steps: []StepStatus{{Name: "s"}}}))
}

// TestEventReporter_Report pins the lean SourceUpdate body (§7.4) and {sourceId}
// substitution; the abort flag is parsed and returned (tf honors it, the ports discard it).
func TestEventReporter_Report(t *testing.T) {
	srv := meshapitest.NewServer(t)
	srv.SeedPatchResponse(meshapitest.PatchResponse{Status: 200, Abort: true})
	r := newTestReporter(srv, "src-uuid")

	msg := "hello"
	abort, err := r.Report(RunStatus{
		RunId:  "run-1",
		Status: SUCCEEDED,
		Steps:  []StepStatus{{Name: "manual", DisplayName: "Manual Block Run", Status: SUCCEEDED, UserMessage: &msg, Outputs: map[string]Output{"k": {Value: "v", Type: "STRING", Sensitive: true}}}},
	})
	require.NoError(t, err)
	require.True(t, abort, "abort flag from the PATCH response is surfaced")

	patches := srv.Patches()
	require.Len(t, patches, 1)
	require.Equal(t, "src-uuid", patches[0].SourceId)

	var upd meshapi.SourceUpdateDTO
	require.NoError(t, json.Unmarshal(patches[0].Body, &upd))
	require.Equal(t, "SUCCEEDED", upd.Status)
	require.Len(t, upd.Steps, 1)
	require.Equal(t, "manual", upd.Steps[0].Id)
	require.Equal(t, "SUCCEEDED", upd.Steps[0].Status)
	require.Equal(t, "hello", upd.Steps[0].UserMessage)
	require.Empty(t, upd.Steps[0].SystemMessage, "nil message serializes as absent (omitempty), not null")
	require.Equal(t, "v", upd.Steps[0].Outputs["k"].Value)
	require.True(t, upd.Steps[0].Outputs["k"].Sensitive)
}

func TestEventReporter_MissingRunId(t *testing.T) {
	srv := meshapitest.NewServer(t)
	r := newTestReporter(srv, "src")
	require.Error(t, r.Register(RunStatus{}))
	_, err := r.Report(RunStatus{})
	require.Error(t, err)
}

func TestEventReporter_TransportErrors(t *testing.T) {
	t.Run("register 500", func(t *testing.T) {
		srv := meshapitest.NewServer(t)
		srv.SeedRegisterResponse(500)
		require.Error(t, newTestReporter(srv, "src").Register(RunStatus{RunId: "r", Steps: []StepStatus{{Name: "s"}}}))
	})
	t.Run("report 500", func(t *testing.T) {
		srv := meshapitest.NewServer(t)
		srv.SeedPatchResponse(meshapitest.PatchResponse{Status: 500})
		_, err := newTestReporter(srv, "src").Report(RunStatus{RunId: "r"})
		require.Error(t, err)
	})
}

// fakeRunPatcher lets us drive the response-body-ignored branch (a body that is not the
// abort DTO must simply read abort=false, never error — Kotlin ignores the body).
type fakeRunPatcher struct{ body []byte }

func (f fakeRunPatcher) RegisterSource(string, meshapi.RegistrationDTO) error { return nil }
func (f fakeRunPatcher) PatchStatus(string, string, any) ([]byte, error)      { return f.body, nil }

func TestEventReporter_IgnoresUnparsableResponseBody(t *testing.T) {
	r := NewReporter(fakeRunPatcher{body: []byte("not json")}, "src", repLog())
	abort, err := r.Report(RunStatus{RunId: "r", Status: SUCCEEDED})
	require.NoError(t, err)
	require.False(t, abort)
}
