package dispatch

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/meshapitest"
)

func newTestClaimClient(srv *meshapitest.Server) *RunClaimClient {
	return NewRunClaimClient(
		srv.URL,
		"runner-uuid",
		"run-controller",
		meshapi.BasicAuth{Username: "user", Password: "pass"},
		meshapi.Identity{Name: "run-controller", Version: "test"},
		NewMetricsCollector(),
	)
}

func TestRunClaimClient_Claim_ReturnsClaimedRun(t *testing.T) {
	srv := meshapitest.NewServer(t)
	dto := &meshapi.RunDetailsDTO{Metadata: meshapi.RunMetaDTO{Uuid: "run-1"}}
	srv.SeedRun(dto)

	c := newTestClaimClient(srv)
	run, err := c.Claim()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if run.Id != "run-1" {
		t.Errorf("expected run id %q, got %q", "run-1", run.Id)
	}
	if run.Details.Metadata.Uuid != "run-1" {
		t.Errorf("expected Details to carry the fetched dto, got %+v", run.Details)
	}

	decoded, err := base64.StdEncoding.DecodeString(run.RawJson)
	if err != nil {
		t.Fatalf("expected RawJson to be valid base64: %v", err)
	}
	var roundTripped meshapi.RunDetailsDTO
	if err := json.Unmarshal(decoded, &roundTripped); err != nil {
		t.Fatalf("expected RawJson to decode back to a RunDetailsDTO: %v", err)
	}
	if roundTripped.Metadata.Uuid != "run-1" {
		t.Errorf("expected RawJson to round-trip the claimed run, got %+v", roundTripped)
	}

	claims := srv.Claims()
	if len(claims) != 1 {
		t.Fatalf("expected 1 captured claim, got %d", len(claims))
	}
	if claims[0].RunnerUuid != "runner-uuid" {
		t.Errorf("expected forRunnerUuid=runner-uuid, got %q", claims[0].RunnerUuid)
	}
	if got := claims[0].Header.Get("X-Block-Runner-Node-Id"); got != "run-controller-runner-uuid" {
		t.Errorf("expected node id %q, got %q", "run-controller-runner-uuid", got)
	}
}

func TestRunClaimClient_Claim_NoRunIsNotAnError(t *testing.T) {
	srv := meshapitest.NewServer(t) // empty queue -> 404 (default noRunStatus)

	c := newTestClaimClient(srv)
	_, err := c.Claim()
	if err == nil {
		t.Fatal("expected an error (404 no-run signal)")
	}
	if !isNoRunError(err) {
		t.Errorf("expected a 404 no-run error, got %v", err)
	}
}

func TestRunClaimClient_Claim_ServerErrorIsNotSwallowed(t *testing.T) {
	srv := meshapitest.NewServer(t, meshapitest.WithNoRunStatus(http.StatusInternalServerError))

	c := newTestClaimClient(srv)
	_, err := c.Claim()
	if err == nil {
		t.Fatal("expected an error for a 500 response")
	}
	if isNoRunError(err) {
		t.Error("a 500 must not classify as the 404 no-run signal")
	}
}

func TestRunClaimClient_RegisterSource_SendsValidationStep(t *testing.T) {
	srv := meshapitest.NewServer(t)
	c := newTestClaimClient(srv)

	if err := c.RegisterSource("run-42"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	regs := srv.Registers()
	if len(regs) != 1 {
		t.Fatalf("expected 1 captured register, got %d", len(regs))
	}
	if regs[0].RunId != "run-42" {
		t.Errorf("expected run id %q, got %q", "run-42", regs[0].RunId)
	}
	if regs[0].Registration.Source.Id != "runner-uuid" {
		t.Errorf("expected source id %q, got %q", "runner-uuid", regs[0].Registration.Source.Id)
	}
	if len(regs[0].Registration.Steps) != 1 || regs[0].Registration.Steps[0].Id != "validation" {
		t.Errorf("expected exactly one 'validation' step, got %+v", regs[0].Registration.Steps)
	}
}

func TestRunClaimClient_UpdateRunStatus_SendsStatusUpdateDTO(t *testing.T) {
	srv := meshapitest.NewServer(t)
	c := newTestClaimClient(srv)

	if err := c.UpdateRunStatus("run-7", "FAILED", "boom", "boom"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	patches := srv.Patches()
	if len(patches) != 1 {
		t.Fatalf("expected 1 captured patch, got %d", len(patches))
	}
	if patches[0].RunId != "run-7" {
		t.Errorf("expected run id %q, got %q", "run-7", patches[0].RunId)
	}
	if patches[0].SourceId != "runner-uuid" {
		t.Errorf("expected source id %q, got %q", "runner-uuid", patches[0].SourceId)
	}

	var dto meshapi.StatusUpdateDTO
	if err := json.Unmarshal(patches[0].Body, &dto); err != nil {
		t.Fatalf("failed to decode patch body: %v", err)
	}
	if dto.Status == nil || *dto.Status != "FAILED" {
		t.Errorf("expected status FAILED, got %+v", dto.Status)
	}
	if len(dto.Steps) != 1 || dto.Steps[0].Id != "validation" {
		t.Errorf("expected exactly one 'validation' step, got %+v", dto.Steps)
	}
}
