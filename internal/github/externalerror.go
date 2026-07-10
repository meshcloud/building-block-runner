package github

import (
	"errors"
	"fmt"
)

// externalCallError is the github-local twin of the Kotlin MeshHttpException (06B's
// ExternalCallError, D3). 06B is NOT yet landed in this branch's base (phase-6a-manual),
// so the shape it promised does not exist in internal/meshapi yet; per the plan's
// autonomy rule this package carries its own value type with the exact fields §2.6 needs
// (RequestUrl + StatusCode + ResponseBody) and the consolidation step reconciles it to the
// shared meshapi.ExternalCallError. Recorded as a run-log uncertainty.
//
// It is raised only by the two installation calls (installation id + token); its §2.6
// system message differs from the generic path, selected via errors.As.
type externalCallError struct {
	UserMessage   string
	SystemMessage string
	StatusCode    int
	RequestUrl    string
	ResponseBody  string
}

func (e *externalCallError) Error() string {
	if e.SystemMessage != "" {
		return e.SystemMessage
	}
	return fmt.Sprintf("github API call to %s failed with status %d: %s", e.RequestUrl, e.StatusCode, e.ResponseBody)
}

// asExternalCallError reports whether err (or something it wraps) is an *externalCallError.
func asExternalCallError(err error) (*externalCallError, bool) {
	var ece *externalCallError
	if errors.As(err, &ece) {
		return ece, true
	}
	return nil, false
}
