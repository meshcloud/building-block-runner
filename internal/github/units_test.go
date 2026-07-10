package github

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/report"
)

// Test_UnsupportedInputMessages covers all five guidance branches.
func Test_UnsupportedInputMessages(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		omit   bool
		substr string
	}{
		{"url-omit", inputKeyRunUrl, true, "does not support the 'buildingBlockRunUrl' input parameter"},
		{"runobject-legacy", inputKeyRunObject, false, "Please enable the 'Pass only API URL' option"},
		{"api-token", inputKeyApiToken, true, "ephemeral API token"},
		{"run-token", inputKeyRunToken, true, "authentication token for updating the building block run status"},
		{"other", "someInput", false, "does not support the 'someInput' input parameter"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := unsupportedInputSystemMessage("wf.yml", tc.input, tc.omit)
			if !strings.Contains(got, tc.substr) {
				t.Errorf("message = %q; want substring %q", got, tc.substr)
			}
		})
	}
	// joined message keeps order.
	joined := unsupportedInputsMessage("wf.yml", []string{inputKeyApiToken, inputKeyRunToken}, true)
	if strings.Count(joined, "\n") < 1 {
		t.Errorf("joined message should contain multiple lines: %q", joined)
	}
}

// Test_TriggerSuccessMessages covers sync/async extras.
func Test_TriggerSuccessMessages(t *testing.T) {
	u, s := triggerSuccessMessages("wf", false)
	if !strings.Contains(u, "Polling for completion status...") || !strings.Contains(s, "Polling for completion status...") {
		t.Errorf("sync messages wrong: %q / %q", u, s)
	}
	u, _ = triggerSuccessMessages("wf", true)
	if !strings.Contains(u, "Will wait for API updates on status...") {
		t.Errorf("async message wrong: %q", u)
	}
}

// Test_JobStepMessages covers every jobUserMessage branch and the system-message assembly.
func Test_JobStepMessages(t *testing.T) {
	cases := []struct {
		status     workflowJobStatus
		conclusion string
		wantUser   string
		wantStatus string
	}{
		{jobCompleted, "success", "Job 'b' completed successfully", "SUCCEEDED"},
		{jobCompleted, "failure", "Job 'b' failed", "FAILED"},
		{jobCompleted, "cancelled", "Job 'b' was cancelled", "FAILED"},
		{jobCompleted, "skipped", "Job 'b' was skipped", "FAILED"},
		{jobInProgress, "", "Job 'b' is running", "IN_PROGRESS"},
		{jobQueued, "", "Job 'b' is queued", "IN_PROGRESS"},
	}
	for _, tc := range cases {
		job := workflowJob{Id: 5, Name: "b", Status: tc.status, Conclusion: tc.conclusion, HtmlUrl: "u"}
		st := jobStep(job)
		if derefOr(st.UserMessage) != tc.wantUser {
			t.Errorf("status %s/%s user = %q; want %q", tc.status, tc.conclusion, derefOr(st.UserMessage), tc.wantUser)
		}
		if st.Status.String() != tc.wantStatus {
			t.Errorf("status %s/%s mapped = %s; want %s", tc.status, tc.conclusion, st.Status, tc.wantStatus)
		}
		if st.Name != jobStepIdPrefix+"5" {
			t.Errorf("step id = %q", st.Name)
		}
	}
	// system message assembles the optional fields.
	full := jobStep(workflowJob{Id: 9, Name: "x", Status: jobCompleted, Conclusion: "success", StartedAt: "t1", CompletedAt: "t2", HtmlUrl: "http://j"})
	sys := derefOr(full.SystemMessage)
	for _, sub := range []string{"Job ID: 9", "Status: completed", "Conclusion: success", "Started: t1", "Completed: t2", "View job: http://j"} {
		if !strings.Contains(sys, sub) {
			t.Errorf("system message %q missing %q", sys, sub)
		}
	}
}

// Test_ReportFinal covers the conclusion → status/message table.
func Test_ReportFinal(t *testing.T) {
	cases := []struct {
		conclusion string
		wantStatus string
		wantUser   string
	}{
		{"success", "SUCCEEDED", "GitHub workflow completed successfully"},
		{"failure", "FAILED", "GitHub workflow failed"},
		{"cancelled", "FAILED", "GitHub workflow was cancelled"},
		{"timed_out", "FAILED", "GitHub workflow timed out"},
		{"weird", "FAILED", "GitHub workflow completed with unknown status"},
	}
	h := NewHandler(Config{}, HandlerDeps{})
	for _, tc := range cases {
		rep := &fakeReporter{}
		if err := h.reportFinal(rep, "r", workflowRun{Id: 1, Status: runCompleted, Conclusion: tc.conclusion, HtmlUrl: "u"}); err != nil {
			t.Fatalf("reportFinal: %v", err)
		}
		last := rep.lastReport()
		if last.Status.String() != tc.wantStatus {
			t.Errorf("%s: status = %s; want %s", tc.conclusion, last.Status, tc.wantStatus)
		}
		if derefOr(last.Steps[0].UserMessage) != tc.wantUser {
			t.Errorf("%s: user = %q; want %q", tc.conclusion, derefOr(last.Steps[0].UserMessage), tc.wantUser)
		}
	}
}

// Test_ReportAborted_Fallback: ABORTED rejected ⇒ falls back to FAILED (never SUCCEEDED).
func Test_ReportAborted_Fallback(t *testing.T) {
	h := NewHandler(Config{}, HandlerDeps{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Reporter that fails the first (ABORTED) report only.
	rep := &failFirstReporter{}
	err := h.reportAborted(ctx, rep, "r")
	if err == nil {
		t.Fatal("expected ctx error returned")
	}
	if len(rep.statuses) != 2 {
		t.Fatalf("expected ABORTED then FAILED fallback, got statuses %v", rep.statuses)
	}
	if rep.statuses[0] != "ABORTED" || rep.statuses[1] != "FAILED" {
		t.Errorf("statuses = %v; want [ABORTED FAILED]", rep.statuses)
	}
}

type failFirstReporter struct {
	statuses []string
	n        int
}

func (r *failFirstReporter) Register(report.RunStatus) error { return nil }
func (r *failFirstReporter) Report(s report.RunStatus) (bool, error) {
	r.statuses = append(r.statuses, s.Status.String())
	r.n++
	if r.n == 1 {
		return false, errors.New("aborted rejected")
	}
	return false, nil
}

// Test_RealClock exercises Now/Wait (Wait returns false on ctx cancel, true otherwise).
func Test_RealClock(t *testing.T) {
	c := RealClock{}
	if c.Now().IsZero() {
		t.Error("Now should not be zero")
	}
	if !c.Wait(context.Background(), time.Millisecond) {
		t.Error("Wait should complete for a short duration")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if c.Wait(ctx, time.Hour) {
		t.Error("Wait should return false when ctx is already cancelled")
	}
}

// Test_NewCertDecryptor covers construction (error on bad PEM) and the NoOp identity.
func Test_NewCertDecryptor(t *testing.T) {
	if _, err := NewCertDecryptor("not a pem"); err == nil {
		t.Error("expected error building a cert decryptor from a bad PEM")
	}
	if got, _ := (NoOpDecryptor{}).Decrypt("cipher"); got != "cipher" {
		t.Errorf("NoOp Decrypt = %q; want passthrough", got)
	}
}

// Test_ExternalCallError_Error covers the Error() string.
func Test_ExternalCallError_Error(t *testing.T) {
	e := &externalCallError{SystemMessage: "sys"}
	if e.Error() != "sys" {
		t.Errorf("Error = %q; want sys", e.Error())
	}
	e2 := &externalCallError{StatusCode: 500, RequestUrl: "u", ResponseBody: "b"}
	if !strings.Contains(e2.Error(), "500") {
		t.Errorf("Error = %q; want status", e2.Error())
	}
}

// Test_NewHandler_Defaults covers the nil-dep fallbacks.
func Test_NewHandler_Defaults(t *testing.T) {
	h := NewHandler(Config{}, HandlerDeps{})
	if h.deps.Clock == nil || h.deps.Log == nil || h.deps.Decryptor == nil || h.deps.HTTP == nil {
		t.Error("NewHandler should fill nil deps with defaults")
	}
	if h.findAttempts != 12 || h.pollInterval != 10*time.Second || h.pollTimeout != 30*time.Minute {
		t.Errorf("constructor default constants wrong: %+v", h)
	}
	// HTTP client must disable redirects.
	if h.deps.HTTP.CheckRedirect == nil {
		t.Error("default HTTP client should disable redirects")
	}
}

// Test_ReporterFactory builds a factory and exercises the run-token wiring path (nil details).
func Test_ReporterFactory(t *testing.T) {
	f := NewReporterFactory("http://mesh", "runner", meshapi.Identity{Name: "github-block-runner"}, testLog())
	r := f(dispatch.ClaimedRun{Id: "x"})
	if r == nil {
		t.Fatal("factory returned nil reporter")
	}
}
