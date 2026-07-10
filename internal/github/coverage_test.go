package github

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/meshcloud/building-block-runner/internal/meshapi"
)

// Test_Client_ParseErrors covers the json-unmarshal error branches of each call.
func Test_Client_ParseErrors(t *testing.T) {
	stub := newGithubStub(t)
	stub.installation = jsonHandler(200, `not json`)
	stub.token = jsonHandler(200, `not json`)
	stub.listRuns = jsonHandler(200, `not json`)
	stub.getRun = jsonHandler(200, `not json`)
	stub.listJobs = jsonHandler(200, `not json`)
	gc := newGithubClient(stub.url(), stub.server.Client())

	if _, err := gc.installationId("t", "o", "r"); err == nil {
		t.Error("installationId should error on invalid JSON")
	}
	if _, err := gc.installationToken("t", "1"); err == nil {
		t.Error("installationToken should error on invalid JSON")
	}
	if _, err := gc.listWorkflowRuns("t", "o", "r", "w"); err == nil {
		t.Error("listWorkflowRuns should error on invalid JSON")
	}
	if _, err := gc.workflowRunByID("t", "o", "r", 1); err == nil {
		t.Error("workflowRunByID should error on invalid JSON")
	}
	if _, err := gc.workflowJobs("t", "o", "r", 1); err == nil {
		t.Error("workflowJobs should error on invalid JSON")
	}
}

// Test_Client_UnknownJobStatus covers the job-status validation error into the retry path.
func Test_Client_UnknownJobStatus(t *testing.T) {
	stub := newGithubStub(t)
	stub.listJobs = jsonHandler(200, `{"jobs":[{"id":1,"name":"x","status":"bogus","conclusion":null,"html_url":"u"}]}`)
	gc := newGithubClient(stub.url(), stub.server.Client())
	if _, err := gc.workflowJobs("t", "o", "r", 1); err == nil || !strings.Contains(err.Error(), "Unknown workflow job status") {
		t.Fatalf("expected unknown-job-status error, got %v", err)
	}
}

// Test_Client_ListRunsError covers the non-2xx branch on a GET listing (externalCallError).
func Test_Client_ListRunsError(t *testing.T) {
	stub := newGithubStub(t)
	stub.listRuns = jsonHandler(500, `boom`)
	gc := newGithubClient(stub.url(), stub.server.Client())
	if _, err := gc.listWorkflowRuns("t", "o", "r", "w"); err == nil {
		t.Error("expected error on 500 listWorkflowRuns")
	}
	stub.getRun = jsonHandler(500, `boom`)
	if _, err := gc.workflowRunByID("t", "o", "r", 1); err == nil {
		t.Error("expected error on 500 workflowRunByID")
	}
	stub.listJobs = jsonHandler(500, `boom`)
	if _, err := gc.workflowJobs("t", "o", "r", 1); err == nil {
		t.Error("expected error on 500 workflowJobs")
	}
}

// Test_Client_TransportError covers the do() transport-error branch (bad host).
func Test_Client_TransportError(t *testing.T) {
	gc := newGithubClient("http://127.0.0.1:0", nil)
	if _, err := gc.installationId("t", "o", "r"); err == nil {
		t.Error("expected transport error against an unreachable host")
	}
}

// Test_ReadRawInputs_Fallbacks covers the details fallback + bad-base64 + undecodable paths.
func Test_ReadRawInputs_Fallbacks(t *testing.T) {
	details := &meshapi.RunDetailsDTO{}
	details.Spec.BuildingBlock.Spec.Inputs = []meshapi.BuildingBlockInputSpecDTO{{Key: "k", Value: "v", Type: "STRING"}}

	// empty raw ⇒ details fallback.
	out, err := decodeAndDecryptInputs("", details, NoOpDecryptor{}, testLog())
	if err != nil || len(out) != 1 || out[0].Key != "k" {
		t.Fatalf("empty-raw fallback failed: %v / %+v", err, out)
	}
	// invalid base64 ⇒ warn + details fallback.
	out, err = decodeAndDecryptInputs("!!!notbase64!!!", details, NoOpDecryptor{}, testLog())
	if err != nil || len(out) != 1 {
		t.Fatalf("bad-base64 fallback failed: %v / %+v", err, out)
	}
	// valid base64 but undecodable JSON ⇒ warn + details fallback.
	out, err = decodeAndDecryptInputs(encodeRawJSON("{not json"), details, NoOpDecryptor{}, testLog())
	if err != nil || len(out) != 1 {
		t.Fatalf("undecodable-json fallback failed: %v / %+v", err, out)
	}
	// nil details + empty raw ⇒ no inputs.
	out, err = decodeAndDecryptInputs("", nil, NoOpDecryptor{}, testLog())
	if err != nil || len(out) != 0 {
		t.Fatalf("nil-details path failed: %v / %+v", err, out)
	}
}

// Test_ModeARunObject_Errors covers nil-details and bad-impl-type branches.
func Test_ModeARunObject_Errors(t *testing.T) {
	if _, err := modeARunObject(decryptedRun{details: nil}); err == nil {
		t.Error("expected error for nil details")
	}
	// implementation with no/invalid type discriminator.
	d := &meshapi.RunDetailsDTO{}
	d.Spec.Definition.Spec.Implementation = json.RawMessage(`not json`)
	if _, err := modeARunObject(decryptedRun{details: d}); err == nil {
		t.Error("expected error for bad implementation type")
	}
}

// Test_ValueToString covers the nil/bool/default branches.
func Test_ValueToString(t *testing.T) {
	if valueToString(nil) != "" {
		t.Error("nil should render empty")
	}
	if valueToString(true) != "true" {
		t.Error("bool should render true")
	}
	if valueToString(json.Number("42")) != "42" {
		t.Error("json.Number should render its text")
	}
}

// Test_SelectWorkflow covers DESTROY-present and unknown-behavior branches.
func Test_SelectWorkflow(t *testing.T) {
	destroy := "destroy.yml"
	impl := meshapi.GithubImplementation{ApplyWorkflow: "apply.yml", DestroyWorkflow: &destroy}
	if wf, err := selectWorkflow("DESTROY", impl); err != nil || wf != "destroy.yml" {
		t.Errorf("DESTROY = %q,%v; want destroy.yml", wf, err)
	}
	if wf, err := selectWorkflow("DETECT", impl); err != nil || wf != "apply.yml" {
		t.Errorf("DETECT = %q,%v; want apply.yml", wf, err)
	}
	if wf, err := selectWorkflow("SOMETHING", impl); err != nil || wf != "apply.yml" {
		t.Errorf("unknown behavior = %q,%v; want apply.yml", wf, err)
	}
}

// Test_FindRun_CtxCancel covers ctx cancellation during the find phase ⇒ aborted report.
func TestScenario_Github_FindRunCtxCancel(t *testing.T) {
	stub := newGithubStub(t)
	stub.listRuns = jsonHandler(200, `{"workflow_runs":[]}`) // never finds

	ctx, cancel := context.WithCancel(context.Background())
	clock := newFakeClock(testStart)
	clock.onWait = func(call int) {
		if call == 1 {
			cancel()
		}
	}
	h, rep := newTestHandler(t, stub, clock)
	run := runFixture{baseURL: stub.url(), appPem: singleLinePem(t), async: false}.claim(t)

	if err := h.Execute(ctx, run); err == nil {
		t.Fatal("expected ctx error during find phase")
	}
	if rep.lastReport().Status.String() != "ABORTED" {
		t.Errorf("terminal status = %s; want ABORTED", rep.lastReport().Status)
	}
}
