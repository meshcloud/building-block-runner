package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/httpclient"
	"github.com/meshcloud/building-block-runner/internal/report"
)

var testStart = time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)

// Scenario_Github_AsyncRun_ModeA: register → JWT'd installation calls → dispatch (Mode-A
// payload) → one IN_PROGRESS update with SUCCEEDED gh-trigger → NO polling calls (async).
func TestScenario_Github_AsyncRun_ModeA(t *testing.T) {
	stub := newGithubStub(t)
	h, rep := newTestHandler(t, stub, newFakeClock(testStart))
	run := runFixture{baseURL: stub.url(), appPem: singleLinePem(t), async: true}.claim(t)

	if err := h.Execute(context.Background(), run); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(rep.registered) != 1 || len(rep.registered[0].Steps) != 1 || rep.registered[0].Steps[0].Name != StepId {
		t.Fatalf("expected one gh-trigger registration, got %+v", rep.registered)
	}
	if len(rep.reports) != 1 {
		t.Fatalf("expected exactly 1 report (async handover), got %d: %+v", len(rep.reports), rep.reports)
	}
	last := rep.reports[0]
	if last.Status.String() != "IN_PROGRESS" {
		t.Errorf("run status = %s; want IN_PROGRESS", last.Status)
	}
	trigger := stepByName(last, StepId)
	if trigger.Status.String() != "SUCCEEDED" {
		t.Errorf("gh-trigger status = %s; want SUCCEEDED", trigger.Status)
	}
	if got := derefOr(trigger.UserMessage); got != "Triggered GitHub Action 'apply.yml'. Will wait for API updates on status..." {
		t.Errorf("trigger userMessage = %q", got)
	}

	// No polling calls: only installation, token, dispatch (3 GitHub requests).
	if n := len(stub.requests()); n != 3 {
		t.Errorf("expected 3 GitHub calls (no polling), got %d", n)
	}

	// Mode-A: dispatch body carries buildingBlockRun (base64 JSON), ref=main.
	body := dispatchBody(t, stub)
	if _, ok := body.Inputs[inputKeyRunObject]; !ok {
		t.Errorf("Mode-A dispatch missing buildingBlockRun input: %+v", body.Inputs)
	}
	if body.Ref != "main" {
		t.Errorf("ref = %q; want main", body.Ref)
	}
}

// Scenario ModeB: omitRunObjectInput ⇒ dispatch carries buildingBlockRunUrl + tokens.
func TestScenario_Github_AsyncRun_ModeB(t *testing.T) {
	stub := newGithubStub(t)
	h, _ := newTestHandler(t, stub, newFakeClock(testStart))
	inputs := `[{"key":"MESHSTACK_API_TOKEN","value":"tok","type":"STRING","isSensitive":true,"isEnvironment":false},
                {"key":"MESHSTACK_ENDPOINT","value":"https://ep","type":"STRING","isSensitive":false,"isEnvironment":false}]`
	run := runFixture{baseURL: stub.url(), appPem: singleLinePem(t), async: true, omit: true, inputsJSON: inputs}.claim(t)

	if err := h.Execute(context.Background(), run); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	body := dispatchBody(t, stub)
	if body.Inputs[inputKeyRunUrl] != "https://meshstack.example.com/run/run-1" {
		t.Errorf("Mode-B missing/incorrect buildingBlockRunUrl: %+v", body.Inputs)
	}
	if body.Inputs[inputKeyApiToken] != "tok" {
		t.Errorf("Mode-B api token = %q; want tok (decrypted via NoOp)", body.Inputs[inputKeyApiToken])
	}
	if body.Inputs[inputKeyEndpoint] != "https://ep" {
		t.Errorf("Mode-B endpoint missing (api token present): %+v", body.Inputs)
	}
	if _, leaked := body.Inputs[inputKeyRunObject]; leaked {
		t.Errorf("Mode-B must not carry buildingBlockRun: %+v", body.Inputs)
	}
}

// Scenario_Github_FailsBeforeTrigger: register-then-FAILED order + the system message per
// pre-trigger failure (wrong impl / null destroy / bad base URL / bad PEM / bad token perms
// / installation error).
func TestScenario_Github_FailsBeforeTrigger(t *testing.T) {
	tests := []struct {
		name        string
		fixture     runFixture
		mutate      func(*githubStub)
		wantSubstr  string
		wantMeshMsg bool // "Request: …\nGitHub responded…"
	}{
		{
			name:       "wrong-impl-type",
			fixture:    runFixture{implType: "GITLAB_CICD"},
			wantSubstr: "The building block implementation of run run-1 was not of expected type.",
		},
		{
			name:       "null-destroy-workflow",
			fixture:    runFixture{behavior: "DESTROY", destroyNull: true},
			wantSubstr: nullWorkflowMessage,
		},
		{
			name:       "empty-base-url",
			fixture:    runFixture{baseURL: ""},
			wantSubstr: "URL should not be empty",
		},
		{
			name:       "bad-pem",
			fixture:    runFixture{appPem: "not-a-valid-pem"},
			wantSubstr: genericErrorPrefix,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stub := newGithubStub(t)
			if tc.fixture.baseURL == "" && tc.name != "empty-base-url" {
				tc.fixture.baseURL = stub.url()
			}
			// Paths that pass the auth chain (null-destroy) need a real PEM; bad-pem sets its own.
			if tc.fixture.appPem == "" {
				tc.fixture.appPem = singleLinePem(t)
			}
			h, rep := newTestHandler(t, stub, newFakeClock(testStart))
			if tc.mutate != nil {
				tc.mutate(stub)
			}

			if err := h.Execute(context.Background(), tc.fixture.claim(t)); err != nil {
				t.Fatalf("Execute returned error (want nil after reported FAILED): %v", err)
			}

			// register-before-validation.
			if len(rep.registered) != 1 {
				t.Fatalf("expected exactly one registration before the FAILED update")
			}
			if len(rep.reports) != 1 {
				t.Fatalf("expected exactly one FAILED report, got %d", len(rep.reports))
			}
			last := rep.reports[0]
			if last.Status.String() != "FAILED" {
				t.Errorf("run status = %s; want FAILED", last.Status)
			}
			trigger := stepByName(last, StepId)
			if derefOr(trigger.UserMessage) != failUserMessage {
				t.Errorf("user message = %q; want %q", derefOr(trigger.UserMessage), failUserMessage)
			}
			if !strings.Contains(derefOr(trigger.SystemMessage), tc.wantSubstr) {
				t.Errorf("system message = %q; want substring %q", derefOr(trigger.SystemMessage), tc.wantSubstr)
			}
		})
	}
}

// Scenario: installation call 404 ⇒ FAILED with the MeshHttpException message.
func TestScenario_Github_InstallationError(t *testing.T) {
	stub := newGithubStub(t)
	stub.installation = jsonHandler(404, `{"message":"Not Found"}`)
	h, rep := newTestHandler(t, stub, newFakeClock(testStart))
	run := runFixture{baseURL: stub.url(), appPem: singleLinePem(t)}.claim(t)

	if err := h.Execute(context.Background(), run); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	msg := derefOr(stepByName(rep.lastReport(), StepId).SystemMessage)
	if !strings.HasPrefix(msg, "Request: ") || !strings.Contains(msg, "GitHub responded with status: 404 and body:") {
		t.Errorf("system message = %q; want the MeshHttpException shape", msg)
	}
}

// Scenario: permission gate (actions:read) ⇒ FAILED generic with the missing-write message.
func TestScenario_Github_PermissionGate(t *testing.T) {
	stub := newGithubStub(t)
	stub.token = jsonHandler(200, `{"token":"t","permissions":{"actions":"read"}}`)
	h, rep := newTestHandler(t, stub, newFakeClock(testStart))
	run := runFixture{baseURL: stub.url(), appPem: singleLinePem(t)}.claim(t)

	if err := h.Execute(context.Background(), run); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	msg := derefOr(stepByName(rep.lastReport(), StepId).SystemMessage)
	if !strings.Contains(msg, "missing write permissions for actions") {
		t.Errorf("system message = %q; want the permission-gate text", msg)
	}
}

// Scenario: trigger UnsupportedInput ⇒ FAILED with the joined guidance message.
func TestScenario_Github_UnsupportedInput(t *testing.T) {
	stub := newGithubStub(t)
	stub.dispatch = jsonHandler(422, `{"message":"Unexpected inputs provided: [\"buildingBlockRun\"]"}`)
	h, rep := newTestHandler(t, stub, newFakeClock(testStart))
	run := runFixture{baseURL: stub.url(), appPem: singleLinePem(t)}.claim(t) // Mode A (omit=false)

	if err := h.Execute(context.Background(), run); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	msg := derefOr(stepByName(rep.lastReport(), StepId).SystemMessage)
	if !strings.Contains(msg, "does not support the 'buildingBlockRun' input parameter") {
		t.Errorf("system message = %q; want the buildingBlockRun guidance", msg)
	}
	if rep.lastReport().Status.String() != "FAILED" {
		t.Errorf("status = %s; want FAILED", rep.lastReport().Status)
	}
}

// Scenario: trigger Error (500) ⇒ FAILED with the api-error message.
func TestScenario_Github_TriggerApiError(t *testing.T) {
	stub := newGithubStub(t)
	stub.dispatch = jsonHandler(500, `boom`)
	h, rep := newTestHandler(t, stub, newFakeClock(testStart))
	run := runFixture{baseURL: stub.url(), appPem: singleLinePem(t)}.claim(t)

	if err := h.Execute(context.Background(), run); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	msg := derefOr(stepByName(rep.lastReport(), StepId).SystemMessage)
	if msg != "GitHub API returned status 500 when triggering workflow: boom" {
		t.Errorf("system message = %q", msg)
	}
}

// Scenario: register transport failure ⇒ Execute returns the error, no reports
// (infrastructure failure).
func TestScenario_Github_RegisterFailurePropagates(t *testing.T) {
	stub := newGithubStub(t)
	rep := &fakeReporter{failRegister: errors.New("boom-register")}
	h := NewHandler(Config{Uuid: "r"}, HandlerDeps{
		Reporters: func(dispatch.ClaimedRun) report.Reporter { return rep },
		HTTP:      stub.server.Client(),
		Clock:     newFakeClock(testStart),
	})
	run := runFixture{baseURL: stub.url(), appPem: singleLinePem(t)}.claim(t)

	if err := h.Execute(context.Background(), run); err == nil {
		t.Fatal("expected Execute to return the register transport error")
	}
	if len(rep.reports) != 0 {
		t.Errorf("expected no status reports after a register failure, got %d", len(rep.reports))
	}
}

// TestScenario_Github_DispatchRedirect_NeverReachesSecondServer pins WithNoRedirect on the
// dispatch trigger POST: its JSON body carries MESHSTACK_API_TOKEN/MESHSTACK_RUN_TOKEN, so a
// followed 307 would resend them to whatever server the Location header names. The redirect
// target must never be reached; the trigger fails cleanly instead (FAILED, classified on the
// 307 response itself, not a second request).
func TestScenario_Github_DispatchRedirect_NeverReachesSecondServer(t *testing.T) {
	var secondServerHit atomic.Bool
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		secondServerHit.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer second.Close()

	stub := newGithubStub(t)
	stub.dispatch = func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, second.URL, http.StatusTemporaryRedirect)
	}

	// A client wired with the shared singleton's sentinel CheckRedirect, so dispatchWorkflow's
	// per-request meshapi.WithNoRedirect is what stops the redirect (not a client-level ban).
	hc := &http.Client{CheckRedirect: httpclient.SentinelCheckRedirect}
	h, rep := newTestHandlerWithHTTP(t, newFakeClock(testStart), hc)
	run := runFixture{baseURL: stub.url(), appPem: singleLinePem(t)}.claim(t)

	if err := h.Execute(context.Background(), run); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if secondServerHit.Load() {
		t.Error("the redirect target must never receive the dispatch payload")
	}
	last := rep.lastReport()
	if last.Status.String() != "FAILED" {
		t.Errorf("status = %s; want FAILED", last.Status)
	}
	if !strings.Contains(derefOr(stepByName(last, StepId).SystemMessage), "307") {
		t.Errorf("system message = %q; want it to mention the 307 status", derefOr(stepByName(last, StepId).SystemMessage))
	}
}

// TestScenario_Github_DispatchFails_NeverRetried pins that the dispatch POST is not in the
// shared client's retry whitelist: even a retry-capable transport that would otherwise retry
// a transport-retryable 503 leaves the dispatch call as exactly one POST, failing hard instead
// of risking a double-dispatched workflow.
func TestScenario_Github_DispatchFails_NeverRetried(t *testing.T) {
	stub := newGithubStub(t)
	var calls atomic.Int32
	stub.dispatch = func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}

	retryClient := &http.Client{
		Transport: httpclient.NewRetryTransport(nil, httpclient.RetryOptions{
			MaxRetries:       2,
			Backoff:          httpclient.ExponentialBackoff{MinWait: time.Millisecond, MaxWait: 2 * time.Millisecond},
			WhitelistedPosts: nil,
		}, nil),
	}

	h, rep := newTestHandlerWithHTTP(t, newFakeClock(testStart), retryClient)
	run := runFixture{baseURL: stub.url(), appPem: singleLinePem(t)}.claim(t)

	if err := h.Execute(context.Background(), run); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("dispatch called %d times; want 1 (the trigger POST must never be retried)", calls.Load())
	}
	last := rep.lastReport()
	if last.Status.String() != "FAILED" {
		t.Errorf("status = %s; want FAILED", last.Status)
	}
	if !strings.Contains(derefOr(stepByName(last, StepId).SystemMessage), "503") {
		t.Errorf("system message = %q; want it to mention 503", derefOr(stepByName(last, StepId).SystemMessage))
	}
}

// TestScenario_Github_WarnsOnSensitiveInputs pins the single sensitive-input-forwarding
// WARN (meshapi.SensitiveInputKeys): inputs arrive already decrypted at the dispatch
// boundary, so the handler no longer decrypts anything, but it still logs which keys it is
// about to forward.
func TestScenario_Github_WarnsOnSensitiveInputs(t *testing.T) {
	stub := newGithubStub(t)
	var logBuf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logBuf, nil))
	rep := &fakeReporter{}
	h := NewHandler(Config{Uuid: "runner"}, HandlerDeps{
		Reporters: func(dispatch.ClaimedRun) report.Reporter { return rep },
		HTTP:      stub.server.Client(),
		Clock:     newFakeClock(testStart),
		Log:       log,
	})
	inputs := `[{"key":"secretInput","value":"plaintext-secret","type":"STRING","isSensitive":true,"isEnvironment":false},
                {"key":"plainInput","value":"v","type":"STRING","isSensitive":false,"isEnvironment":false}]`
	run := runFixture{baseURL: stub.url(), appPem: singleLinePem(t), async: true, inputsJSON: inputs}.claim(t)

	if err := h.Execute(context.Background(), run); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	out := logBuf.String()
	if !strings.Contains(out, "forwarding sensitive inputs") || !strings.Contains(out, "secretInput") {
		t.Errorf("expected a WARN naming the sensitive input key, got log output: %s", out)
	}
	if strings.Contains(out, "plainInput") {
		t.Errorf("non-sensitive input key must not be named in the sensitive-keys WARN: %s", out)
	}
}

// dispatchInputBody is the parsed dispatch payload for assertions.
type dispatchInputBody struct {
	Ref    string            `json:"ref"`
	Inputs map[string]string `json:"inputs"`
}

func dispatchBody(t *testing.T, stub *githubStub) dispatchInputBody {
	t.Helper()
	for _, r := range stub.requests() {
		if strings.HasSuffix(r.Path, "/dispatches") {
			var b dispatchInputBody
			if err := json.Unmarshal([]byte(r.Body), &b); err != nil {
				t.Fatalf("parsing dispatch body: %v", err)
			}
			return b
		}
	}
	t.Fatal("no dispatch request recorded")
	return dispatchInputBody{}
}
