package github

import (
	"errors"
	"fmt"
)

// externalCallError is the github-local twin of the Kotlin MeshHttpException. This
// package carries its own value type with the exact fields the system message needs
// (RequestUrl + StatusCode + ResponseBody).
//
// It is raised only by the two installation calls (installation id + token); its
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
