package azdevops

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/meshapitest"
)

const testUuid = "a9786b14-ecfe-44dd-b04c-2bcfd326aa23"

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// implFixture is the minimal azure-devops implementation fixture; refName/async are the two
// fields tests vary most.
type implFixture struct {
	PersonalAccessToken string
	Async               bool
	RefName             *string
}

// buildRun assembles a ClaimedRun the way the runner type would (Details parsed + RawJson =
// base64 of the same bytes, so readInputs re-reads with UseNumber for fidelity) with an
// AZURE_DEVOPS implementation pointed at the given fake ADO base URL.
func buildRun(t *testing.T, adoBaseUrl string, token string, impl implFixture, inputs []meshapi.BuildingBlockInputSpecDTO) dispatch.ClaimedRun {
	t.Helper()

	implDTO := meshapi.AzureDevOpsImplementation{
		Type:                "AZURE_DEVOPS",
		AzureDevOpsBaseUrl:  adoBaseUrl,
		Organization:        "myorg",
		Project:             "myproj",
		PipelineId:          "42",
		PersonalAccessToken: impl.PersonalAccessToken,
		Async:               impl.Async,
		RefName:             impl.RefName,
	}
	implRaw, err := json.Marshal(implDTO)
	require.NoError(t, err)

	dto := &meshapi.Run{
		Metadata: meshapi.RunMetaDTO{Uuid: testUuid},
		Spec: meshapi.RunSpecDTO{
			RunToken:      token,
			Behavior:      "APPLY",
			BuildingBlock: meshapi.BuildingBlockSpecDTO{Spec: meshapi.BuildingBlockDetailsSpecDTO{Inputs: inputs}},
			Definition:    meshapi.DefinitionSpecDTO{Spec: meshapi.DefinitionDetailsSpecDTO{Implementation: implRaw}},
		},
	}
	raw, err := json.Marshal(dto)
	require.NoError(t, err)

	return dispatch.ClaimedRun{
		Id:      dispatch.RunId(testUuid),
		Type:    meshapi.RunnerTypeAzureDevOpsPipeline,
		Run:     dto,
		RawJson: base64.StdEncoding.EncodeToString(raw),
	}
}

func factoryFor(srv *meshapitest.Server) ReporterFactory {
	return NewReporterFactory(srv.URL, testUuid, meshapi.Identity{Name: "azure-devops-block-runner", Version: "test"}, testLog())
}

func decodePatch(t *testing.T, body []byte) meshapi.SourceUpdateDTO {
	t.Helper()
	var u meshapi.SourceUpdateDTO
	require.NoError(t, json.Unmarshal(body, &u))
	return u
}

// --- fake Azure DevOps server (sequenced, path-routed) --------------------------------------

type adoResp struct {
	status int
	body   string
}

// seqADO is a real httptest.Server standing in for Azure DevOps, with per-endpoint response
// queues so poll_test.go can script a run's state across multiple GET calls.
type seqADO struct {
	*httptest.Server

	mu          sync.Mutex
	triggerResp adoResp
	runQueue    []adoResp
	tlQueue     []adoResp
	requests    []capturedReq
}

func newSeqADO(t *testing.T) *seqADO {
	t.Helper()
	s := &seqADO{triggerResp: adoResp{status: 200, body: `{"id":1,"state":"inProgress","result":null,"createdDate":"now"}`}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(r.Body)
		}
		s.mu.Lock()
		s.requests = append(s.requests, capturedReq{Method: r.Method, URL: r.URL.String(), Header: r.Header.Clone(), Body: body})
		s.mu.Unlock()

		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/pipelines/"):
			s.write(w, s.triggerResp)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/build/builds/"):
			s.write(w, s.pop(&s.tlQueue))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pipelines/"):
			s.write(w, s.pop(&s.runQueue))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *seqADO) write(w http.ResponseWriter, resp adoResp) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.status)
	_, _ = w.Write([]byte(resp.body))
}

func (s *seqADO) pop(queue *[]adoResp) adoResp {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(*queue) == 0 {
		return adoResp{status: 500, body: `{"message":"seqADO: queue exhausted -- test bug"}`}
	}
	next := (*queue)[0]
	if len(*queue) > 1 {
		*queue = (*queue)[1:]
	}
	return next
}

func (s *seqADO) SeedRun(status int, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runQueue = append(s.runQueue, adoResp{status: status, body: body})
}

func (s *seqADO) SeedTimeline(status int, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tlQueue = append(s.tlQueue, adoResp{status: status, body: body})
}

func (s *seqADO) Requests() []capturedReq {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]capturedReq(nil), s.requests...)
}

// --- fake Clock -------------------------------------------------------------------------

// fakeClock is instant (After fires immediately, no real sleep) and deterministic: Now()
// returns a fixed value unless bumpAfterFirstCall is set, in which case every call after the
// first adds that bump -- exactly enough to make a pollCompletion's initial deadline check
// (the 2nd Now() call) read as already-expired relative to the 1st call's deadline,
// without any wall-clock drift or goroutine coordination.
type fakeClock struct {
	mu                 sync.Mutex
	now                time.Time
	calls              int
	bumpAfterFirstCall time.Duration
	// afterBlocks makes After() return a channel that is never signalled, so only
	// ctx.Done() can ever unblock the poller's select -- used by the ctx-cancel test to
	// avoid a race between two simultaneously-ready channels.
	afterBlocks bool
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.calls > 1 {
		return c.now.Add(c.bumpAfterFirstCall)
	}
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	if c.afterBlocks {
		return make(chan time.Time)
	}
	ch := make(chan time.Time, 1)
	ch <- c.now.Add(d)
	return ch
}

// newTestHandler builds a Handler wired to the fake ADO client (via the real *http.Client
// dialing srv) and the given reporter factory/clock, mirroring the runner type wiring.
func newTestHandler(reporters ReporterFactory, clock Clock) Handler {
	return NewHandler(Config{BaseConfig: config.BaseConfig{Uuid: testUuid}}, HandlerDeps{
		Reporters: reporters,
		HTTP:      NewHTTPClient(0, nil),
		Clock:     clock,
		Log:       testLog(),
	})
}

func execute(t *testing.T, h Handler, run dispatch.ClaimedRun) error {
	t.Helper()
	return h.Execute(context.Background(), run)
}

// --- crypto test fixtures (checked-in test key pair, shared style with internal/tf) --------

func mustReadTestKeyHandler(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../resources/test.key")
	require.NoError(t, err)
	return string(data)
}
