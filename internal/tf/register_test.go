package tf

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/meshcloud/building-block-runner/internal/config"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestTfDispatchCadence_MatchesDeletedManager freezes the tf dispatch cadence to the values the
// deleted Manager/Worker polling loop used, so operators see no scheduling change: a 10s
// idle poll (the old NORUN_WORKER_DELAY, wired into LoopConfig.PollInterval by the cmd/bbrunner
// assembly) and a 60s back-off after a claim-fetch error (the old FAILED_WORKER_DELAY, wired into
// LoopConfig.ClaimBackoff). The third leg -- immediate re-claim once a run completes -- is provided
// by the generic loop via InProcess.Done() (dispatch.InProcess signals completion so the loop
// drains again at once rather than waiting a full PollInterval), and is exercised in the dispatch
// package's capacity suite.
func TestTfDispatchCadence_MatchesDeletedManager(t *testing.T) {
	if NORUN_WORKER_DELAY != 10*time.Second {
		t.Errorf("idle poll cadence = %v, want 10s (former Manager NORUN_WORKER_DELAY)", NORUN_WORKER_DELAY)
	}
	if FAILED_WORKER_DELAY != 60*time.Second {
		t.Errorf("fetch-error back-off = %v, want 60s (former Manager FAILED_WORKER_DELAY)", FAILED_WORKER_DELAY)
	}
}

// TestClaimNodePostfix_Frozen pins the observable "<uuid>-worker-1" claim requester suffix (an
// observable fetch header) that the cmd/bbrunner assembly wires via dispatch.WithRequester.
func TestClaimNodePostfix_Frozen(t *testing.T) {
	if ClaimNodePostfix != "worker-1" {
		t.Errorf("claim node postfix = %q, want %q (former Worker FetchRunDetails node-id)", ClaimNodePostfix, "worker-1")
	}
}

func TestRegister_NilRegistration_IsNoop(t *testing.T) {
	cfg := TfRunnerConfig{BaseConfig: config.BaseConfig{Uuid: "u", Api: config.Api{Url: "http://localhost"}}}
	if err := Register(testLogger(), cfg, meshapi.BasicAuth{Username: "u", Password: "p"}); err != nil {
		t.Errorf("expected nil (no registration section), got %v", err)
	}
}

func TestRegister_InvalidCapability_ReturnsError(t *testing.T) {
	cfg := TfRunnerConfig{
		BaseConfig:   config.BaseConfig{Uuid: "runner-uuid", Api: config.Api{Url: "http://localhost", Username: "u", Password: "p"}},
		Registration: &TfRegistrationConfig{Capability: "NOT_A_REAL_TYPE"},
	}
	if err := Register(testLogger(), cfg, meshapi.BasicAuth{Username: "u", Password: "p"}); err == nil {
		t.Fatal("expected an error for an invalid registration capability")
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
		BaseConfig: config.BaseConfig{Uuid: "runner-uuid", Api: config.Api{Url: srv.URL}},
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
		BaseConfig:   config.BaseConfig{Uuid: "runner-uuid", Api: config.Api{Url: srv.URL}},
		Registration: &TfRegistrationConfig{Capability: string(meshapi.RunnerTypeAll)},
	}

	err := Register(testLogger(), cfg, meshapi.BasicAuth{Username: "u", Password: "p"})
	if err == nil || !strings.Contains(err.Error(), "create it via the meshStack UI") {
		t.Fatalf("expected an actionable 404 error, got %v", err)
	}
}
