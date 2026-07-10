package gitlab

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// ExternalCallError is the MeshHttpException equivalent (umbrella §4 row 14, 06A §4.4):
// a typed error for a failed call to the external system (GitLab), carrying the fields
// GitLabBlockRunnerService needs to build its FAILED step update. It ships here, in
// package gitlab, with its first consumer -- 06C/06D define their own identical-shaped
// type per the umbrella ruling ("per-package typed error with the same fields"); a shared
// package would be P3 ceremony for a 5-field struct until review decides otherwise (flag
// §16.9 of plan 06B).
type ExternalCallError struct {
	// UserMessage/SystemMessage classify the failure (§2.3 rows A1-A4). They surface only
	// via Error() -> the process log: the service reports the SAME userMessage
	// ("Could not trigger the GitLab pipeline") for every classification, and builds its
	// own systemMessage from StatusCode/ResponseBody directly (row B) -- the classified
	// pair here is diagnostic, not wire content (flag §16.1).
	UserMessage   string
	SystemMessage string
	StatusCode    int
	RequestUrl    string
	ResponseBody  string
}

// Error reproduces MeshHttpException.buildMessage byte-for-byte (MeshHttpException.kt:16-26):
// "<user>[ - <system>] [HTTP <code> <url>]".
func (e *ExternalCallError) Error() string {
	msg := e.UserMessage
	if e.SystemMessage != "" {
		msg += " - " + e.SystemMessage
	}
	return fmt.Sprintf("%s [HTTP %d %s]", msg, e.StatusCode, e.RequestUrl)
}

// gitlabErrorBody is the shape GitLab's own error responses use
// (GitLabClient.kt:23-33): {"message":{"base":[...]}}. Message is a pointer so a JSON
// document that parses but lacks the "message" object is distinguishable from one that
// carries it (Kotlin's non-null constructor parameter fails deserialization entirely in
// that case -- row A2 -- whereas a bare struct field would silently zero-value it).
type gitlabErrorBody struct {
	Message *struct {
		Base []string `json:"base"`
	} `json:"message"`
}

// identityVerificationRequiredMessage is the exact base-string GitLab returns when the
// account triggering the pipeline needs identity verification (GitLabClient.kt:30-33).
const identityVerificationRequiredMessage = "Identity verification is required in order to run CI jobs"

func (b gitlabErrorBody) isIdentityVerificationRequired() bool {
	if b.Message == nil {
		return false
	}
	for _, s := range b.Message.Base {
		if s == identityVerificationRequiredMessage {
			return true
		}
	}
	return false
}

// classifyGitlabError reproduces GitLabClient.kt:69-107's four-way classification (§2.3):
// A1 404, A2 undeserializable error body, A3 identity-verification-required, A4 any other
// non-2xx. Only the wire-visible fields (StatusCode/ResponseBody, via the caller's own
// FAILED-step message) are a contract; UserMessage/SystemMessage here are log-only
// (flag §16.1).
func classifyGitlabError(statusCode int, requestURL string, body []byte) error {
	if statusCode == 404 {
		return &ExternalCallError{
			UserMessage:   "GitLab pipeline could not be triggered successfully. Please contact support.",
			SystemMessage: "GitLab reported 404, which can happen if you have entered a wrong projectId.",
			StatusCode:    statusCode,
			RequestUrl:    requestURL,
			ResponseBody:  string(body),
		}
	}

	var errBody gitlabErrorBody
	if err := json.Unmarshal(body, &errBody); err != nil || errBody.Message == nil {
		return &ExternalCallError{
			UserMessage:  "There was a problem while communicating with GitLab.",
			StatusCode:   statusCode,
			RequestUrl:   requestURL,
			ResponseBody: string(body),
		}
	}

	if errBody.isIdentityVerificationRequired() {
		return &ExternalCallError{
			UserMessage: "There is a problem with the pipeline trigger token. Please contact support.",
			SystemMessage: "Your GitLab account is not verified and can not trigger a pipeline. " +
				"Please visit GitLab and verify your account first.",
			StatusCode:   statusCode,
			RequestUrl:   requestURL,
			ResponseBody: string(body),
		}
	}

	return &ExternalCallError{
		UserMessage:   "There was an error communicating with GitLab.",
		SystemMessage: fmt.Sprintf("GitLab did not process the request, and responded with: %s", compactJSON(body)),
		StatusCode:    statusCode,
		RequestUrl:    requestURL,
		ResponseBody:  string(body),
	}
}

// compactJSON re-renders body as compact JSON for the A4 log line. Unlike Kotlin's
// `$gitlabError` (a private data class's default toString -> "ClassName@hash", pure log
// garbage, GitLabClient.kt:101-107), this logs the actual parsed body -- not pinned (logs
// are not contract), flag §16.2 of plan 06B. body is already known to parse as JSON at the
// call site, but this falls back to the raw bytes defensively rather than panic.
func compactJSON(body []byte) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, body); err != nil {
		return string(body)
	}
	return buf.String()
}
