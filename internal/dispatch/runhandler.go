package dispatch

import "context"

// RunHandler executes exactly one claimed run to completion, in-process. It is the
// in-process counterpart of a k8s job template: where KubernetesJobDispatcher hands a run to
// a Job pod, InProcess hands it to a RunHandler goroutine. The tf handler (internal/tf) is
// the first implementation; the four Kotlin ports become further implementations,
// which is why this interface is deliberately minimal -- everything a handler needs travels
// in ClaimedRun plus its own constructor-injected deps, so adding a handler
// never edits this package.
//
// Contract:
//   - run.RawJson / run.Details are still ENCRYPTED. The handler decrypts per run, inside its
//     own goroutine, so plaintext for run A never lives on the shared loop path next to run
//     B. This placement (handler-side, not loop-side) is also what preserves tf's pinned
//     decrypt-failure UX -- a mismatch fails the run through the engine, not the loop.
//   - all run-scoped reporting MUST use the run's own runToken (run.Details.Spec.RunToken),
//     never the runner's process credentials, so N concurrent runs can never
//     cross-authenticate. The runner's main creds are for claiming only.
//   - the handler owns its own execution timeout, derived from ctx (tf: TfCommandTimeoutMins).
//   - ctx is cancelled when the process is shutting down and the grace period expires
//     (InProcess.Wait). A handler that observes cancellation MUST still leave the run in a
//     TERMINAL state -- report ABORTED (falling back to FAILED, never SUCCEEDED) -- so the
//     coordinator never sees a stale IN_PROGRESS that only clears on its long timeout.
//   - a non-nil returned error means an infrastructure failure *around* execution (e.g. a
//     pre-mutation setup failure such as working-dir creation). A run that reached the tool
//     and failed at run level is reported FAILED by the handler itself and returns nil: the
//     run outcome is metered from the handler's terminal status (today's
//     Worker.tfExecution, worker.go:154-159), NOT from this return value. InProcess therefore
//     never classifies success/failure from the error -- it only logs a non-nil error as an
//     out-of-band diagnostic.
type RunHandler interface {
	Execute(ctx context.Context, run ClaimedRun) error
}
