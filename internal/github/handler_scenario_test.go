package github

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
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

// Scenario_Github_FailsBeforeTrigger: register-then-FAILED order + the §2.6 message per
// pre-trigger failure (wrong impl / null destroy / bad base URL / bad PEM / bad token perms
// / installation error).
func TestScenario_Github_FailsBeforeTrigger(t *testing.T) {
	tests := []struct {
		name        string
		fixture     runFixture
		mutate      func(*githubStub)
		wantSubstr  string
		wantMeshMsg bool // §2.6 "Request: …\nGitHub responded…"
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

			// register-before-validation (G-P9).
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

// Scenario: installation call 404 ⇒ FAILED with the §2.6 MeshHttpException message.
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
		t.Errorf("system message = %q; want the §2.6 MeshHttpException shape", msg)
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

// Scenario: register transport failure ⇒ Execute returns the error, no reports (A1
// infrastructure failure).
func TestScenario_Github_RegisterFailurePropagates(t *testing.T) {
	stub := newGithubStub(t)
	rep := &fakeReporter{failRegister: errors.New("boom-register")}
	h := NewHandler(Config{Uuid: "r"}, HandlerDeps{
		Reporters: func(dispatch.ClaimedRun) report.Reporter { return rep },
		Decryptor: NoOpDecryptor{},
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
