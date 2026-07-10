// Package meshapitest is the shared meshfed-API mock server (PLAN_DETAIL_03_shared_core.md
// §5.7, D6): a real net/http/httptest.Server exposing the frozen runner-facing endpoints
// (claim POST, register-source POST, status PATCH, artifact download GET) with seedable
// runs and captured requests. D6 prefers this real-HTTP style over a hand-rolled
// http.RoundTripper because it exercises the client's actual transport (headers, retry,
// media types) end to end; every runner-type's integration suite (this phase's client
// test, the phase-5 concurrency suite, phase-6 per-persona tests, the phase-7 opt-in
// controller e2e) dials the same mock instead of hand-rolling its own fake.
//
// Test-only helper: not part of the D6 coverage gate (§9) and not itself a wire contract —
// it stands in for the real meshfed API in tests, it does not define the API.
package meshapitest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
)

// PatchResponse is what the mock returns for one status-PATCH call: the HTTP status plus
// the runAborted response body the real endpoint uses to carry the abort flag (D9) —
// including the already-aborted case (409 {runAborted:true}), which is just Status:409,
// Abort:true here.
type PatchResponse struct {
	Status int
	Abort  bool
}

// CapturedRequest is the transport-level evidence every mock endpoint records, so tests
// can assert the frozen wire details (media types, node-id, auth headers) without
// re-deriving them from httptest internals at each call site.
type CapturedRequest struct {
	Method string
	Path   string
	Header http.Header
	Body   []byte
}

// ClaimRequest is one captured claim (FetchRun) call.
type ClaimRequest struct {
	CapturedRequest
	RunnerUuid string // the forRunnerUuid query parameter
}

// RegisterRequest is one captured register-source call, with its body already decoded —
// registration has exactly one frozen shape (RegistrationDTO, §8).
type RegisterRequest struct {
	CapturedRequest
	RunId        string
	Registration meshapi.RegistrationDTO
}

// PatchRequest is one captured status-PATCH call. Body is left undecoded: two frozen
// shapes exist on this endpoint (StatusUpdateDTO / RunStatusUpdateDTO, §8) and the mock
// does not privilege either — decode it into whichever DTO the test under test expects.
type PatchRequest struct {
	CapturedRequest
	RunId    string
	SourceId string
}

// Server is a hermetic stand-in for the meshfed runner-facing API. Zero value is not
// usable; construct via NewServer.
type Server struct {
	*httptest.Server

	mu sync.Mutex

	claimQueue  []*meshapi.RunDetailsDTO
	claimStatus int
	noRunStatus int

	registerResponses []int
	registerDefault   int

	patchResponses []PatchResponse
	patchDefault   PatchResponse

	artifacts map[string][]byte

	claims    []ClaimRequest
	registers []RegisterRequest
	patches   []PatchRequest
}

// Option configures a Server at construction time.
type Option func(*Server)

// WithClaimStatus overrides the status returned by a successful claim (default 200).
func WithClaimStatus(status int) Option {
	return func(s *Server) { s.claimStatus = status }
}

// WithNoRunStatus overrides the status returned when a claim finds the seeded-run queue
// empty (default 404). D9 pins both 404 and 409 as "no run" on the claim endpoint, so
// tests proving the 409 branch reconfigure this.
func WithNoRunStatus(status int) Option {
	return func(s *Server) { s.noRunStatus = status }
}

// NewServer starts the mock on an ephemeral local port and registers its shutdown with
// tb.Cleanup, mirroring httptest.NewServer's own lifecycle so callers never leak listeners.
func NewServer(tb testing.TB, opts ...Option) *Server {
	tb.Helper()

	s := &Server{
		claimStatus:     http.StatusOK,
		noRunStatus:     http.StatusNotFound,
		registerDefault: http.StatusOK,
		patchDefault:    PatchResponse{Status: http.StatusOK, Abort: false},
		artifacts:       make(map[string][]byte),
	}
	for _, opt := range opts {
		opt(s)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/meshobjects/meshbuildingblockruns/create", s.handleClaim)
	mux.HandleFunc("POST /api/meshobjects/meshbuildingblockruns/{runId}/status/source", s.handleRegister)
	mux.HandleFunc("PATCH /api/meshobjects/meshbuildingblockruns/{runId}/status/source/{sourceId}", s.handlePatch)
	mux.HandleFunc("GET /artifacts/{id}", s.handleArtifact)

	s.Server = httptest.NewServer(mux)
	tb.Cleanup(s.Close)

	return s
}

// SeedRun enqueues a run to be returned by the next claim call (FIFO). Seeding several
// runs lets one Server stand in for multiple poll cycles, or the N-simultaneous-runs
// scenarios the phase-5 concurrency suite needs.
func (s *Server) SeedRun(run *meshapi.RunDetailsDTO) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claimQueue = append(s.claimQueue, run)
}

// SeedRegisterResponse queues one status for the next register-source call (FIFO) — e.g.
// http.StatusConflict to exercise the "409-on-register = success" pin (D9). Once the queue
// drains, calls fall back to the constructor default (200).
func (s *Server) SeedRegisterResponse(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registerResponses = append(s.registerResponses, status)
}

// SeedPatchResponse queues one response for the next status-PATCH call (FIFO), letting
// tests drive the abort flag (D9) and the already-aborted 409 {runAborted:true} case.
func (s *Server) SeedPatchResponse(resp PatchResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.patchResponses = append(s.patchResponses, resp)
}

// SeedArtifact registers data under id and returns the full download URL for it, ready to
// stand in for a planArtifact link or be passed straight to meshapi.Client.DownloadArtifact.
func (s *Server) SeedArtifact(id string, data []byte) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.artifacts[id] = data
	return s.URL + "/artifacts/" + id
}

// Claims returns every captured claim (FetchRun) request, in call order.
func (s *Server) Claims() []ClaimRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ClaimRequest(nil), s.claims...)
}

// Registers returns every captured register-source request, in call order.
func (s *Server) Registers() []RegisterRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]RegisterRequest(nil), s.registers...)
}

// Patches returns every captured status-PATCH request, in call order.
func (s *Server) Patches() []PatchRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]PatchRequest(nil), s.patches...)
}

func (s *Server) handleClaim(w http.ResponseWriter, r *http.Request) {
	captured := capture(r)
	runnerUuid := r.URL.Query().Get("forRunnerUuid")

	s.mu.Lock()
	var run *meshapi.RunDetailsDTO
	if len(s.claimQueue) > 0 {
		run = s.claimQueue[0]
		s.claimQueue = s.claimQueue[1:]
	}
	status := s.claimStatus
	s.claims = append(s.claims, ClaimRequest{CapturedRequest: captured, RunnerUuid: runnerUuid})
	s.mu.Unlock()

	if run == nil {
		w.WriteHeader(s.noRunStatus)
		return
	}

	body, err := json.Marshal(run)
	if err != nil {
		// A seeded fixture that cannot marshal is a broken test, not mock behavior under test.
		panic(fmt.Sprintf("meshapitest: marshal seeded run: %v", err))
	}
	w.Header().Set("Content-Type", meshapi.BlockRunMediaTypeV1)
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	captured := capture(r)
	runId := r.PathValue("runId")

	var reg meshapi.RegistrationDTO
	if err := json.Unmarshal(captured.Body, &reg); err != nil {
		http.Error(w, fmt.Sprintf("meshapitest: decode registration body: %v", err), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	status := s.registerDefault
	if len(s.registerResponses) > 0 {
		status = s.registerResponses[0]
		s.registerResponses = s.registerResponses[1:]
	}
	s.registers = append(s.registers, RegisterRequest{CapturedRequest: captured, RunId: runId, Registration: reg})
	s.mu.Unlock()

	w.WriteHeader(status)
}

func (s *Server) handlePatch(w http.ResponseWriter, r *http.Request) {
	captured := capture(r)
	runId := r.PathValue("runId")
	sourceId := r.PathValue("sourceId")

	s.mu.Lock()
	resp := s.patchDefault
	if len(s.patchResponses) > 0 {
		resp = s.patchResponses[0]
		s.patchResponses = s.patchResponses[1:]
	}
	s.patches = append(s.patches, PatchRequest{CapturedRequest: captured, RunId: runId, SourceId: sourceId})
	s.mu.Unlock()

	body, err := json.Marshal(meshapi.RunUpdateResponseDTO{Abort: resp.Abort})
	if err != nil {
		panic(fmt.Sprintf("meshapitest: marshal patch response: %v", err))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.Status)
	_, _ = w.Write(body)
}

func (s *Server) handleArtifact(w http.ResponseWriter, r *http.Request) {
	capture(r)
	id := r.PathValue("id")

	s.mu.Lock()
	data, ok := s.artifacts[id]
	s.mu.Unlock()

	if !ok {
		http.Error(w, "meshapitest: no artifact seeded for id "+id, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// capture snapshots a request's method/path/headers/body for later assertions, restoring
// r.Body so the calling handler can still decode it normally.
func capture(r *http.Request) CapturedRequest {
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	return CapturedRequest{
		Method: r.Method,
		Path:   r.URL.Path,
		Header: r.Header.Clone(),
		Body:   body,
	}
}
