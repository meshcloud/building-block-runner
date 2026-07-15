package github

import (
	"net/http"
	"strings"
	"testing"

	"github.com/meshcloud/building-block-runner/internal/meshapi"
)

// Test_ClassifyDispatchResponse pins the 422 heuristic table: status ×
// body → outcome, incl. the "Unexpected inputs provided" + name-containment rule.
func Test_ClassifyDispatchResponse(t *testing.T) {
	recognized := []string{inputKeyRunUrl, inputKeyRunObject, inputKeyApiToken, inputKeyRunToken}

	tests := []struct {
		name        string
		status      int
		body        string
		wantOutcome dispatchOutcome
		wantNames   []string
	}{
		{"204-success", 204, "", dispatchSuccess, nil},
		{"200-success", 200, "ok", dispatchSuccess, nil},
		{
			"422-unsupported-single",
			422,
			`{"message":"Unexpected inputs provided: [\"buildingBlockRun\"]"}`,
			dispatchUnsupportedInput,
			[]string{inputKeyRunObject},
		},
		{
			// Note: "buildingBlockRunUrl" contains "buildingBlockRun" as a substring, so the
			// Kotlin contains-heuristic matches both — a faithful quirk. This body lists the two
			// system tokens instead to assert clean multi-match sorting.
			"422-unsupported-multiple-sorted",
			422,
			`Unexpected inputs provided: MESHSTACK_RUN_TOKEN and MESHSTACK_API_TOKEN`,
			dispatchUnsupportedInput,
			[]string{inputKeyApiToken, inputKeyRunToken}, // sorted: MESHSTACK_API_TOKEN < MESHSTACK_RUN_TOKEN
		},
		{
			"422-no-recognized-name",
			422,
			`Unexpected inputs provided: someOtherInput`,
			dispatchAPIError,
			nil,
		},
		{
			"422-without-phrase",
			422,
			`buildingBlockRun is invalid`,
			dispatchAPIError,
			nil,
		},
		{"500", 500, "boom", dispatchAPIError, nil},
		{"401", 401, "bad creds", dispatchAPIError, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyDispatchResponse(tc.status, tc.body, recognized)
			if got.outcome != tc.wantOutcome {
				t.Fatalf("outcome = %v; want %v", got.outcome, tc.wantOutcome)
			}
			if tc.wantOutcome == dispatchUnsupportedInput {
				if strings.Join(got.unsupportedNames, ",") != strings.Join(tc.wantNames, ",") {
					t.Errorf("names = %v; want %v", got.unsupportedNames, tc.wantNames)
				}
			}
			if tc.wantOutcome == dispatchAPIError {
				if got.statusCode != tc.status || got.body != tc.body {
					t.Errorf("apiError carried status=%d body=%q; want %d / %q", got.statusCode, got.body, tc.status, tc.body)
				}
			}
		})
	}
}

// Test_Client_Headers_And_Paths pins the wire shape of the five calls: common headers
// (Accept, X-GitHub-Api-Version, Bearer) and the correct paths incl. per_page=5.
func Test_Client_Headers_And_Paths(t *testing.T) {
	stub := newGithubStub(t)
	stub.getRun = jsonHandler(200, `{"id":100,"status":"in_progress","conclusion":null,"created_at":"2026-07-10T10:00:00Z","html_url":"u"}`)
	stub.listRuns = jsonHandler(200, `{"workflow_runs":[{"id":100,"status":"in_progress","conclusion":null,"created_at":"2026-07-10T10:00:00Z","html_url":"u"}]}`)
	stub.listJobs = jsonHandler(200, `{"jobs":[{"id":7,"name":"build","status":"completed","conclusion":"success","started_at":"t1","completed_at":"t2","html_url":"j"}]}`)

	gc := newGithubClient(stub.url(), stub.server.Client(), meshapi.SlogLogger(nil))

	if _, err := gc.installationId("jwt-token", "owner", "repo"); err != nil {
		t.Fatalf("installationId: %v", err)
	}
	if _, err := gc.installationToken("jwt-token", "42"); err != nil {
		t.Fatalf("installationToken: %v", err)
	}
	if _, err := gc.listWorkflowRuns("inst-token", "owner", "repo", "apply.yml"); err != nil {
		t.Fatalf("listWorkflowRuns: %v", err)
	}
	if _, err := gc.workflowRunByID("inst-token", "owner", "repo", 100); err != nil {
		t.Fatalf("workflowRunByID: %v", err)
	}
	if _, err := gc.workflowJobs("inst-token", "owner", "repo", 100); err != nil {
		t.Fatalf("workflowJobs: %v", err)
	}

	reqs := stub.requests()
	if len(reqs) != 5 {
		t.Fatalf("expected 5 requests, got %d", len(reqs))
	}
	for _, r := range reqs {
		if r.Header.Get("Accept") != acceptHeader {
			t.Errorf("%s %s: Accept=%q; want %q", r.Method, r.Path, r.Header.Get("Accept"), acceptHeader)
		}
		if r.Header.Get("X-GitHub-Api-Version") != apiVersionValue {
			t.Errorf("%s %s: missing api-version header", r.Method, r.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("%s %s: Authorization=%q; want Bearer …", r.Method, r.Path, r.Header.Get("Authorization"))
		}
	}
	// installation id/token carry the App JWT; the rest carry the installation token.
	if reqs[0].Header.Get("Authorization") != "Bearer jwt-token" {
		t.Errorf("installation id call Authorization = %q; want Bearer jwt-token", reqs[0].Header.Get("Authorization"))
	}
	if reqs[2].Header.Get("Authorization") != "Bearer inst-token" {
		t.Errorf("listRuns Authorization = %q; want Bearer inst-token", reqs[2].Header.Get("Authorization"))
	}
	// per_page=5 on the runs listing.
	if reqs[2].Query != "per_page=5" {
		t.Errorf("listRuns query = %q; want per_page=5", reqs[2].Query)
	}
	// paths
	if reqs[0].Path != "/repos/owner/repo/installation" {
		t.Errorf("installation path = %q", reqs[0].Path)
	}
	if reqs[1].Path != "/app/installations/42/access_tokens" {
		t.Errorf("token path = %q", reqs[1].Path)
	}
}

// Test_Client_PermissionGate pins the permission gate at the client level: actions!=write ⇒ error carrying
// the frozen "missing write permissions" message, deterministically rendered.
func Test_Client_PermissionGate(t *testing.T) {
	stub := newGithubStub(t)
	stub.token = jsonHandler(200, `{"token":"t","permissions":{"actions":"read","metadata":"read"}}`)
	gc := newGithubClient(stub.url(), stub.server.Client(), meshapi.SlogLogger(nil))

	_, err := gc.installationToken("jwt", "42")
	if err == nil {
		t.Fatal("expected permission-gate error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "missing write permissions for actions") {
		t.Errorf("message = %q; want the missing-write-permissions text", msg)
	}
	if !strings.Contains(msg, "Actual permissions: {actions=read, metadata=read}") {
		t.Errorf("message = %q; want sorted deterministic map rendering", msg)
	}
	// A permission-gate failure is NOT an externalCallError (generic path).
	if _, ok := asExternalCallError(err); ok {
		t.Error("permission-gate error should not be an externalCallError")
	}
}

// Test_Client_ExternalCallError pins the MeshHttpException twin on a non-2xx installation
// call: the message shape and the externalCallError fields.
func Test_Client_ExternalCallError(t *testing.T) {
	stub := newGithubStub(t)
	stub.installation = jsonHandler(404, `{"message":"Not Found"}`)
	gc := newGithubClient(stub.url(), stub.server.Client(), meshapi.SlogLogger(nil))

	_, err := gc.installationId("jwt", "owner", "repo")
	if err == nil {
		t.Fatal("expected error")
	}
	ece, ok := asExternalCallError(err)
	if !ok {
		t.Fatalf("expected *externalCallError, got %T", err)
	}
	if ece.StatusCode != 404 {
		t.Errorf("StatusCode = %d; want 404", ece.StatusCode)
	}
	want := "Request: " + stub.url() + "/repos/owner/repo/installation\nGitHub responded with status: 404 and body: " + `{"message":"Not Found"}`
	if ece.SystemMessage != want {
		t.Errorf("SystemMessage = %q; want %q", ece.SystemMessage, want)
	}
}

// Test_Client_UnknownStatus pins the unknown workflow-run status → error (into the retry
// path).
func Test_Client_UnknownStatus(t *testing.T) {
	stub := newGithubStub(t)
	stub.getRun = jsonHandler(200, `{"id":1,"status":"weird","conclusion":null,"created_at":"t","html_url":"u"}`)
	gc := newGithubClient(stub.url(), stub.server.Client(), meshapi.SlogLogger(nil))

	if _, err := gc.workflowRunByID("t", "o", "r", 1); err == nil || !strings.Contains(err.Error(), "Unknown workflow run status") {
		t.Fatalf("expected unknown-status error, got %v", err)
	}
}

// Test_Client_DispatchContentType pins the sanctioned delta: a single
// Content-Type: application/json (the stray HAL media-type header is dropped).
func Test_Client_DispatchContentType(t *testing.T) {
	stub := newGithubStub(t)
	gc := newGithubClient(stub.url(), stub.server.Client(), meshapi.SlogLogger(nil))
	if _, err := gc.dispatchWorkflow("t", "o", "r", "apply.yml", dispatchPayload{Ref: "main", Inputs: map[string]string{}}, nil); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	reqs := stub.requests()
	cts := reqs[0].Header.Values("Content-Type")
	if len(cts) != 1 || cts[0] != "application/json" {
		t.Errorf("Content-Type = %v; want exactly [application/json]", cts)
	}
	if reqs[0].Method != http.MethodPost || !strings.HasSuffix(reqs[0].Path, "/actions/workflows/apply.yml/dispatches") {
		t.Errorf("dispatch request = %s %s", reqs[0].Method, reqs[0].Path)
	}
}
