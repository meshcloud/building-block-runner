package meshapi

import (
	"errors"
	"fmt"
	"net/http"
)

// HttpError is returned by every client method when the meshfed API answers with a
// non-2xx status. It carries the status code and the (capped) response body so callers
// can both classify the failure (IsNotFound/IsConflict — the frozen no-run / already-
// registered semantics) and surface the server's message.
//
// It replaces the former status-only StatusError and is shaped/named identically to the
// terraform-provider-meshstack client's HttpError so the future shared SDK merge
// has no error-type delta.
type HttpError struct {
	StatusCode int
	// ResponseBody is the response body read on the error path, capped at
	// maxErrorBodyBytes so a huge error response cannot exhaust the runner's RAM.
	ResponseBody []byte
}

func (e HttpError) Error() string {
	if len(e.ResponseBody) == 0 {
		return fmt.Sprintf("unexpected HTTP status: %d", e.StatusCode)
	}
	return fmt.Sprintf("unexpected HTTP status %d: %s", e.StatusCode, string(e.ResponseBody))
}

// IsNotFound reports a 404. On the run-claim POST this is the frozen "no run available"
// signal; on the runner-registration PUT it means "create the runner via the UI".
func (e HttpError) IsNotFound() bool { return e.StatusCode == http.StatusNotFound }

// IsConflict reports a 409. On register-source it means "already registered" (success);
// on the claim POST it means "no run available" (both frozen).
func (e HttpError) IsConflict() bool { return e.StatusCode == http.StatusConflict }

// IsForbidden reports a 403 (provider-aligned; retained for SDK-merge symmetry).
func (e HttpError) IsForbidden() bool { return e.StatusCode == http.StatusForbidden }

// AsHttpError unwraps err (via errors.As) into an HttpError. It is the supported way for
// callers to classify a client failure, and works whether the error was returned bare or
// wrapped with fmt.Errorf("...: %w", ...).
func AsHttpError(err error) (HttpError, bool) {
	var he HttpError
	if errors.As(err, &he) {
		return he, true
	}
	return HttpError{}, false
}
