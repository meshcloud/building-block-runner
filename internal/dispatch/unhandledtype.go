package dispatch

import (
	"fmt"

	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

// The constructors below build the two dispatcher-authored UnhandledTypeError values (type
// declared in dispatch.go, alongside the Dispatcher interface it is returned from). Loop
// reports Message verbatim, using the runner's own PROCESS credentials -- never the claimed
// run's runToken (loop.go's reportRunFailure): fail-fast happens before any handler owns the
// run, so reaching for that run's token would carve an exception into the "runToken =
// executing handler only" invariant; process-credential reporting mirrors the pre-existing
// controller.reportRunFailure pattern (controller parity).

// NewK8sJobUnhandledTypeError builds the k8sjob dispatcher's fail-fast message. The wording
// is byte-identical to today's controller message (a frozen, customer-visible run-status
// string) -- it must not change even though the message is now dispatcher-authored
// rather than assembled inline in processNextRun.
func NewK8sJobUnhandledTypeError(t meshapi.RunnerImplementationType) *UnhandledTypeError {
	return &UnhandledTypeError{
		Type:    t,
		Message: fmt.Sprintf("no implementation handler configured for type '%s'", t),
	}
}

// NewInProcessUnhandledTypeError builds the InProcess dispatcher's fail-fast message. Unlike
// the k8sjob wording, this one is new: a standalone runner that registered a broader
// capability (commonly ALL) than the handlers linked into its binary reaches this path, and
// the message must tell the operator what to do about it -- the frozen k8sjob wording would
// be technically true but not actionable here (two messages for one error type, one
// deliberately kept vague-but-frozen and one deliberately actionable -- they are not
// unified).
func NewInProcessUnhandledTypeError(t meshapi.RunnerImplementationType) *UnhandledTypeError {
	return &UnhandledTypeError{
		Type: t,
		Message: fmt.Sprintf(
			"this runner does not handle run type '%s': no in-process handler is registered for it. "+
				"The run was claimed because the runner's registered capability covers this type - "+
				"register the runner with the concrete capability it supports, or run it on a runner "+
				"that implements '%s'.", t, t),
	}
}
