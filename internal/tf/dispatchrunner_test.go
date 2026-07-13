package tf

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/mgmt"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// meterAndMetrics builds a runner_* meter and a run_controller_* collector on a fresh registry
// via mgmt.NewRegistry (so this tf test never has to import prometheus directly -- tf-test
// depguard forbids it).
func meterAndMetrics(uuid string) (*mgmt.RunMetrics, *dispatch.MetricsCollector) {
	reg := mgmt.NewRegistry()
	return mgmt.NewRunMetrics(reg, uuid), dispatch.NewMetricsCollectorWithRegistry(reg)
}

func TestNewDispatchRunner_AssemblesLoopAndDispatcher(t *testing.T) {
	cfg := TfRunnerConfig{
		RunnerUuid:           "runner-uuid",
		TfParentWorkingDir:   t.TempDir(),
		TfCommandTimeoutMins: 10,
		MaxConcurrentRuns:    2,
		RunApiBackend:        RunApiConfig{Url: "http://localhost", User: "u", Password: "p"},
		// no Registration => never self-registers
	}
	meter, metrics := meterAndMetrics(cfg.RunnerUuid)

	loop, inproc, err := NewDispatchRunner(cfg, testLogger(), nil, NoopDecryptor{}, meter, metrics)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if loop == nil || inproc == nil {
		t.Fatalf("expected a non-nil loop and dispatcher, got loop=%v inproc=%v", loop, inproc)
	}
	if n, _ := inproc.InFlight(); n != 0 {
		t.Errorf("fresh dispatcher should have 0 in flight, got %d", n)
	}
}

func TestNewDispatchRunner_RegistrationFailure_ReturnsError(t *testing.T) {
	cfg := TfRunnerConfig{
		RunnerUuid:         "runner-uuid",
		TfParentWorkingDir: t.TempDir(),
		RunApiBackend:      RunApiConfig{Url: "http://localhost", User: "u", Password: "p"},
		Registration:       &TfRegistrationConfig{Capability: "NOT_A_REAL_TYPE"},
	}
	meter, metrics := meterAndMetrics(cfg.RunnerUuid)

	if _, _, err := NewDispatchRunner(cfg, testLogger(), nil, NoopDecryptor{}, meter, metrics); err == nil {
		t.Fatal("expected an error for an invalid registration capability")
	}
}

func TestRegister_NilRegistration_IsNoop(t *testing.T) {
	cfg := TfRunnerConfig{RunnerUuid: "u", RunApiBackend: RunApiConfig{Url: "http://localhost"}}
	if err := Register(testLogger(), cfg, meshapi.BasicAuth{Username: "u", Password: "p"}); err != nil {
		t.Errorf("expected nil (no registration section), got %v", err)
	}
}

func TestRegister_PutsWifLessDTO(t *testing.T) {
	var gotBody []byte
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := TfRunnerConfig{
		RunnerUuid:    "runner-uuid",
		RunApiBackend: RunApiConfig{Url: srv.URL},
		Registration: &TfRegistrationConfig{
			DisplayName:      "My TF Runner",
			OwnedByWorkspace: "ws-1",
			PublicKey:        "PUBKEY",
			Capability:       string(meshapi.RunnerTypeTerraform),
		},
	}

	if err := Register(testLogger(), cfg, meshapi.BasicAuth{Username: "u", Password: "p"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotMethod != http.MethodPut {
		t.Errorf("expected PUT, got %s", gotMethod)
	}
	if want := "/api/meshobjects/meshbuildingblockrunners/runner-uuid"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	body := string(gotBody)
	for _, want := range []string{`"implementationType":"TERRAFORM"`, `"displayName":"My TF Runner"`, `"publicKey":"PUBKEY"`, `"ownedByWorkspace":"ws-1"`} {
		if !strings.Contains(body, want) {
			t.Errorf("registration body missing %q; body=%s", want, body)
		}
	}
	// A WIF-less standalone registration must NOT carry a workloadIdentityFederation block.
	if strings.Contains(body, "workloadIdentityFederation") {
		t.Errorf("standalone tf registration must be WIF-less; body=%s", body)
	}
}

func TestRegister_NotFound_IsActionableError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := TfRunnerConfig{
		RunnerUuid:    "runner-uuid",
		RunApiBackend: RunApiConfig{Url: srv.URL},
		Registration:  &TfRegistrationConfig{Capability: string(meshapi.RunnerTypeAll)},
	}

	err := Register(testLogger(), cfg, meshapi.BasicAuth{Username: "u", Password: "p"})
	if err == nil || !strings.Contains(err.Error(), "create it via the meshStack UI") {
		t.Fatalf("expected an actionable 404 error, got %v", err)
	}
}
