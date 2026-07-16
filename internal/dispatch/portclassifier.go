package dispatch

import "github.com/meshcloud/building-block-runner/internal/meshapi"

// StandaloneClaimClassifier is the claim-error policy shared by the four
// Kotlin-port runner types (manual/gitlab/azdevops/github), established by the manual template
// It reproduces the Kotlin catch-all: every claim
// failure is treated as "no run this tick" and retried on the next PollInterval — there is
// NO extra backoff (deliberately not the tf type's 60s FAILED_WORKER_DELAY, which is
// why ControllerClaimClassifier stays a separate policy). A 404 is the normal idle-poll
// outcome (not logged); a 409 and any other transport error are logged but still just wait
// for the next tick (LoopConfig.ClaimBackoff stays 0 for these runner types).
func StandaloneClaimClassifier(err error) ClaimOutcome {
	if he, ok := meshapi.AsHttpError(err); ok && he.IsNotFound() {
		return OutcomeNoRun
	}
	return OutcomeNoRunLogged
}
