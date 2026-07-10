package gitlab

import (
	"strings"
	"testing"
)

// Test_ClassifyGitlabError ports the G-P3/G-P4/G-P5 error-taxonomy pins (§2.3): 404, the
// identity-verification body, a generic error body, and an undeserializable body all
// classify to distinct UserMessage/SystemMessage pairs (log-only, G-P4 -- the wire-visible
// shape is uniform and asserted at the handler-scenario level), but all are
// *ExternalCallError with the same StatusCode/ResponseBody a caller needs to build the
// row-B step update.
func Test_ClassifyGitlabError(t *testing.T) {
	cases := []struct {
		name           string
		status         int
		body           string
		wantUserSubstr string
		wantIdentity   bool
	}{
		{
			name:           "404 not found",
			status:         404,
			body:           `anything`,
			wantUserSubstr: "GitLab pipeline could not be triggered successfully",
		},
		{
			name:           "identity verification required",
			status:         403,
			body:           `{"message":{"base":["Identity verification is required in order to run CI jobs"]}}`,
			wantUserSubstr: "problem with the pipeline trigger token",
			wantIdentity:   true,
		},
		{
			name:           "generic error body",
			status:         400,
			body:           `{"message":{"base":["some other validation error"]}}`,
			wantUserSubstr: "error communicating with GitLab",
		},
		{
			name:           "undeserializable body (e.g. HTML 500)",
			status:         500,
			body:           `<html><body>Internal Server Error</body></html>`,
			wantUserSubstr: "problem while communicating with GitLab",
		},
		{
			name:           "valid JSON but no message object",
			status:         422,
			body:           `{"error":"nope"}`,
			wantUserSubstr: "problem while communicating with GitLab",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := classifyGitlabError(c.status, "http://gitlab.example/api/v4/projects/1/trigger/pipeline", []byte(c.body))

			extErr, ok := err.(*ExternalCallError)
			if !ok {
				t.Fatalf("classifyGitlabError returned %T, want *ExternalCallError", err)
			}
			if extErr.StatusCode != c.status {
				t.Errorf("StatusCode = %d, want %d", extErr.StatusCode, c.status)
			}
			if extErr.ResponseBody != c.body {
				t.Errorf("ResponseBody = %q, want %q", extErr.ResponseBody, c.body)
			}
			if !strings.Contains(extErr.UserMessage, c.wantUserSubstr) {
				t.Errorf("UserMessage = %q, want substring %q", extErr.UserMessage, c.wantUserSubstr)
			}

			// Error() reproduces MeshHttpException's buildMessage shape.
			if !strings.Contains(extErr.Error(), extErr.UserMessage) {
				t.Errorf("Error() = %q missing UserMessage", extErr.Error())
			}
		})
	}
}

// Test_GitlabErrorBody_IsIdentityVerificationRequired covers the predicate directly,
// including the nil-Message defensive branch.
func Test_GitlabErrorBody_IsIdentityVerificationRequired(t *testing.T) {
	if (gitlabErrorBody{}).isIdentityVerificationRequired() {
		t.Error("zero-value body must not report identity-verification-required")
	}
}
