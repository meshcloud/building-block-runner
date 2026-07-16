package github

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/httpclient"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/meshapitest"
)

// runAt renders a workflow-run JSON with a created_at offset from testStart.
func runAt(status, conclusion string, offset time.Duration) string {
	const id int64 = 100
	created := testStart.Add(offset).Format(time.RFC3339)
	concl := "null"
	if conclusion != "" {
		concl = `"` + conclusion + `"`
	}
	idStr := strconv.FormatInt(id, 10)
	return `{"id":` + idStr + `,"status":"` + status + `","conclusion":` + concl +
		`,"created_at":"` + created + `","html_url":"https://gh/run/` + idStr + `"}`
}

// Scenario_Github_SyncRun_PollsJobsToCompletion: fake clock, staged run/job sequences;
// asserts job step ids, first-batch trigger step, the completed-job re-report quirk,
// and the terminal SUCCEEDED update.
func TestScenario_Github_SyncRun_PollsJobsToCompletion(t *testing.T) {
	stub := newGithubStub(t)
	// listRuns: a run created just after trigger (within the 30s window).
	stub.listRuns = jsonHandler(200, `{"workflow_runs":[`+runAt("in_progress", "", 1*time.Second)+`]}`)
	// getWorkflowRun: in_progress, then completed/success.
	stub.getRun = sequence(200,
		runAt("in_progress", "", 1*time.Second),
		runAt("completed", "success", 1*time.Second),
	)
	// listJobs: first poll one running job; second poll it completed (re-report quirk); final.
	job := func(status, concl string) string {
		c := "null"
		if concl != "" {
			c = `"` + concl + `"`
		}
		return `{"id":7,"name":"build","status":"` + status + `","conclusion":` + c +
			`,"started_at":"t1","completed_at":"t2","html_url":"https://gh/job/7"}`
	}
	stub.listJobs = sequence(200,
		`{"jobs":[`+job("in_progress", "")+`]}`,      // first poll
		`{"jobs":[`+job("completed", "success")+`]}`, // second poll (still re-reported)
		`{"jobs":[`+job("completed", "success")+`]}`, // final snapshot
	)

	h, rep := newTestHandler(t, stub, newFakeClock(testStart))
	run := runFixture{baseURL: stub.url(), appPem: singleLinePem(t), async: false}.claim(t)

	if err := h.Execute(context.Background(), run); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// reports: [0] trigger-success IN_PROGRESS; [1] first job batch (trigger + job);
	// [2] second job batch (completed job re-reported); ... ; terminal SUCCEEDED last.
	if len(rep.reports) < 3 {
		t.Fatalf("expected multiple polling reports, got %d", len(rep.reports))
	}

	// First job batch must include the gh-trigger step (first-batch rule).
	firstBatch := rep.reports[1]
	if stepByName(firstBatch, StepId).Status.String() != "SUCCEEDED" {
		t.Errorf("first job batch missing SUCCEEDED gh-trigger step: %+v", firstBatch.Steps)
	}
	jobStep := stepByName(firstBatch, jobStepIdPrefix+"7")
	if jobStep.Name == "" {
		t.Fatalf("first batch missing job step gh-workflow-job-7")
	}
	if jobStep.DisplayName != "GitHub Job: build" {
		t.Errorf("job display name = %q; want 'GitHub Job: build'", jobStep.DisplayName)
	}

	// The terminal update: SUCCEEDED, only the gh-trigger step, run-completion system message.
	last := rep.reports[len(rep.reports)-1]
	if last.Status.String() != "SUCCEEDED" {
		t.Errorf("terminal status = %s; want SUCCEEDED", last.Status)
	}
	if len(last.Steps) != 1 || last.Steps[0].Name != StepId {
		t.Errorf("terminal update should carry only the gh-trigger step: %+v", last.Steps)
	}
	if got := derefOr(last.Steps[0].UserMessage); got != "GitHub workflow completed successfully" {
		t.Errorf("terminal user message = %q", got)
	}

	// The completed job appears in more than one batch.
	jobBatches := 0
	for _, r := range rep.reports {
		if stepByName(r, jobStepIdPrefix+"7").Name != "" {
			jobBatches++
		}
	}
	if jobBatches < 2 {
		t.Errorf("completed job re-report quirk (G-P4) not observed: job appeared in %d batches", jobBatches)
	}
}

// TestScenario_Github_ListRunsRetriedTransparently pins that a transport-retryable 503 on a
// GET (listWorkflowRuns) is retried inside the shared client's retry transport and never
// surfaces to findRun's own attempt/backoff loop: the run is found on findRun's very first
// attempt, the 503 invisible above the transport layer.
func TestScenario_Github_ListRunsRetriedTransparently(t *testing.T) {
	stub := newGithubStub(t)
	var calls atomic.Int32
	stub.listRuns = func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		jsonHandler(200, `{"workflow_runs":[`+runAt("completed", "success", 1*time.Second)+`]}`)(w, r)
	}

	retryClient := &http.Client{
		Transport: httpclient.NewRetryTransport(nil, httpclient.RetryOptions{
			MaxRetries: 2,
			Backoff:    httpclient.ExponentialBackoff{MinWait: time.Millisecond, MaxWait: 2 * time.Millisecond},
		}, nil),
	}

	h, rep := newTestHandlerWithHTTP(t, newFakeClock(testStart), retryClient)
	run := runFixture{baseURL: stub.url(), appPem: singleLinePem(t), async: false}.claim(t)

	if err := h.Execute(context.Background(), run); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("listRuns called %d times; want 2 (503 then 200, retried within one findRun attempt)", calls.Load())
	}
	if rep.lastReport().Status.String() != "SUCCEEDED" {
		t.Errorf("terminal status = %s; want SUCCEEDED", rep.lastReport().Status)
	}
}

// Scenario_Github_AlreadyCompletedRun (sibling quirk): a found run that is already
// completed skips the poll loop entirely — workflowRunByID is never called.
func TestScenario_Github_AlreadyCompletedRun(t *testing.T) {
	stub := newGithubStub(t)
	stub.listRuns = jsonHandler(200, `{"workflow_runs":[`+runAt("completed", "success", 1*time.Second)+`]}`)
	getRunCalls := 0
	stub.getRun = func(w http.ResponseWriter, r *http.Request) {
		getRunCalls++
		jsonHandler(200, runAt("completed", "success", 0))(w, r)
	}
	stub.listJobs = jsonHandler(200, `{"jobs":[]}`)

	h, rep := newTestHandler(t, stub, newFakeClock(testStart))
	run := runFixture{baseURL: stub.url(), appPem: singleLinePem(t), async: false}.claim(t)

	if err := h.Execute(context.Background(), run); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if getRunCalls != 0 {
		t.Errorf("workflowRunByID called %d times; want 0 (already-completed run skips the loop)", getRunCalls)
	}
	if rep.lastReport().Status.String() != "SUCCEEDED" {
		t.Errorf("terminal status = %s; want SUCCEEDED", rep.lastReport().Status)
	}
}

// Scenario_Github_FindRunTimeout: 12 misses ⇒ FAILED "Could not find…", no terminal leak.
func TestScenario_Github_FindRunTimeout(t *testing.T) {
	stub := newGithubStub(t)
	stub.listRuns = jsonHandler(200, `{"workflow_runs":[]}`) // never finds a matching run

	clock := newFakeClock(testStart)
	h, rep := newTestHandler(t, stub, clock)
	run := runFixture{baseURL: stub.url(), appPem: singleLinePem(t), async: false}.claim(t)

	if err := h.Execute(context.Background(), run); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	last := rep.lastReport()
	if last.Status.String() != "FAILED" {
		t.Errorf("status = %s; want FAILED", last.Status)
	}
	if !strings.Contains(derefOr(stepByName(last, StepId).SystemMessage), "Could not find the triggered workflow run after 12 attempts") {
		t.Errorf("system message = %q; want the not-found message", derefOr(stepByName(last, StepId).SystemMessage))
	}
	// 12 find attempts (each lists runs once).
	listCalls := 0
	for _, r := range stub.requests() {
		if strings.HasSuffix(r.Path, "/runs") {
			listCalls++
		}
	}
	if listCalls != 12 {
		t.Errorf("listWorkflowRuns called %d times; want 12", listCalls)
	}
}

// Scenario_Github_PollTimeout: run stuck in_progress past 30min ⇒ FAILED with the timeout
// message; poll errors before that are retried, not fatal.
func TestScenario_Github_PollTimeout(t *testing.T) {
	stub := newGithubStub(t)
	stub.listRuns = jsonHandler(200, `{"workflow_runs":[`+runAt("in_progress", "", 1*time.Second)+`]}`)
	stub.getRun = jsonHandler(200, runAt("in_progress", "", 1*time.Second)) // never completes
	stub.listJobs = jsonHandler(200, `{"jobs":[]}`)

	clock := newFakeClock(testStart)
	// Jump 31 minutes forward on the first poll wait so the next iteration's timeout check fires.
	clock.onWait = func(call int) {
		if call == 1 {
			clock.now = testStart.Add(31 * time.Minute)
		}
	}
	h, rep := newTestHandler(t, stub, clock)
	run := runFixture{baseURL: stub.url(), appPem: singleLinePem(t), async: false}.claim(t)

	if err := h.Execute(context.Background(), run); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	last := rep.lastReport()
	if last.Status.String() != "FAILED" {
		t.Errorf("status = %s; want FAILED", last.Status)
	}
	if !strings.Contains(derefOr(stepByName(last, StepId).SystemMessage), pollTimeoutSystemDetail) {
		t.Errorf("system message = %q; want the timeout detail", derefOr(stepByName(last, StepId).SystemMessage))
	}
}

// Scenario: ctx cancellation mid-poll ⇒ terminal ABORTED reported, ctx error returned
// (never SUCCEEDED) — the graceful-shutdown amendment.
func TestScenario_Github_CtxCancelReportsAborted(t *testing.T) {
	stub := newGithubStub(t)
	stub.listRuns = jsonHandler(200, `{"workflow_runs":[`+runAt("in_progress", "", 1*time.Second)+`]}`)
	stub.getRun = jsonHandler(200, runAt("in_progress", "", 1*time.Second))
	stub.listJobs = jsonHandler(200, `{"jobs":[]}`)

	ctx, cancel := context.WithCancel(context.Background())
	clock := newFakeClock(testStart)
	// Cancel the ctx as the first poll wait fires, so Wait observes cancellation.
	clock.onWait = func(call int) {
		if call == 1 {
			cancel()
		}
	}
	h, rep := newTestHandler(t, stub, clock)
	run := runFixture{baseURL: stub.url(), appPem: singleLinePem(t), async: false}.claim(t)

	err := h.Execute(ctx, run)
	if err == nil {
		t.Fatal("expected the ctx error to be returned")
	}
	last := rep.lastReport()
	if last.Status.String() != "ABORTED" {
		t.Errorf("terminal status = %s; want ABORTED (never SUCCEEDED)", last.Status)
	}
}

// Scenario: a backend-requested runAborted (T1) on the first job-batch PATCH response stops
// polling promptly (no second listJobs/getRun round) and reports terminal ABORTED, never
// SUCCEEDED/FAILED, with Execute returning nil -- distinct from the shutdown-signal abort
// above (same terminal state, different trigger, no ctx error). Uses meshapitest.Server
// (rather than fakeReporter) so the trigger-success and job-batch PATCHes can be seeded
// independently via SeedPatchResponse.
func TestScenario_Github_BackendAbort_StopsJobPolling(t *testing.T) {
	stub := newGithubStub(t)
	stub.listRuns = jsonHandler(200, `{"workflow_runs":[`+runAt("in_progress", "", 1*time.Second)+`]}`)
	stub.getRun = jsonHandler(200, runAt("in_progress", "", 1*time.Second)) // never completes on its own
	stub.listJobs = jsonHandler(200, `{"jobs":[{"id":7,"name":"build","status":"in_progress","conclusion":null,"html_url":"https://gh/job/7"}]}`)

	srv := meshapitest.NewServer(t)
	srv.SeedPatchResponse(meshapitest.PatchResponse{Status: 200})              // trigger-success update: ok
	srv.SeedPatchResponse(meshapitest.PatchResponse{Status: 200, Abort: true}) // first job batch: backend signals abort

	clock := newFakeClock(testStart)
	h := NewHandler(Config{BaseConfig: config.BaseConfig{Uuid: "runner"}}, HandlerDeps{
		Reporters: NewReporterFactory(srv.URL, "runner", meshapi.Identity{Name: "github-block-runner"}, testLog()),
		HTTP:      stub.server.Client(),
		Clock:     clock,
	})
	run := runFixture{baseURL: stub.url(), appPem: singleLinePem(t), async: false}.claim(t)

	if err := h.Execute(context.Background(), run); err != nil {
		t.Fatalf("Execute: %v (a handled backend abort must return nil, not an error)", err)
	}

	patches := srv.Patches()
	if len(patches) != 3 {
		t.Fatalf("expected 3 patches (trigger-success, job-batch carrying abort, ABORTED follow-up), got %d", len(patches))
	}
	var aborted meshapi.SourceUpdateDTO
	if err := json.Unmarshal(patches[2].Body, &aborted); err != nil {
		t.Fatalf("decoding the terminal patch: %v", err)
	}
	if aborted.Status != "ABORTED" {
		t.Errorf("terminal status = %s; want ABORTED (never SUCCEEDED/FAILED)", aborted.Status)
	}

	listJobsCalls := 0
	for _, r := range stub.requests() {
		if strings.HasSuffix(r.Path, "/jobs") {
			listJobsCalls++
		}
	}
	if listJobsCalls != 1 {
		t.Errorf("listWorkflowJobs called %d times; want 1 (polling must stop right after the abort signal)", listJobsCalls)
	}
}
