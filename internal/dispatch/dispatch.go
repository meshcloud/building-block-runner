// Package dispatch holds the backend-agnostic claim/drain loop and the seam a
// Dispatcher plugs into. It is the dissolution target of the former
// internal/controller package: the polling/capacity/
// fail-fast machinery that used to be entangled with Kubernetes specifics now lives here,
// generalized so any backend (k8s Jobs today, in-process goroutines later) can plug in.
package dispatch

import (
	"github.com/meshcloud/building-block-runner/internal/meshapi"
)

// RunId identifies one claimed run end-to-end (claim -> dispatch -> fail-fast report). A
// named type keeps it from being silently swapped with other string-typed ids (runner
// uuids, node ids) at call sites.
type RunId string

// ClaimedRun is one claimed run as fetched from the meshfed API. Sensitive values inside
// RawJson/Details are still encrypted -- decryption is placed inside the owning
// Dispatcher, not the loop, so each dispatch path keeps its own pinned decrypt-failure
// behavior.
type ClaimedRun struct {
	Id RunId
	// Type is resolved by Loop from Details before Dispatch is called (ToRunnerType of the
	// definition's implementation type); it is the zero value until then.
	Type    meshapi.RunnerImplementationType
	Details *meshapi.RunDetailsDTO
	// RawJson is the base64 of the claimed bytes exactly as fetched (still encrypted).
	RawJson string
}

// Dispatcher places one claimed run for execution. KubernetesJobDispatcher
// (internal/k8sjob) is today's only implementation; an in-process dispatcher is a later
// step.
type Dispatcher interface {
	// InFlight reports how many runs this dispatcher currently has in flight, so Loop's
	// capacity guard can decide how many more runs to claim this cycle.
	InFlight() (int, error)
	// Dispatch places run for execution. A non-nil *UnhandledTypeError means no
	// handler/template exists for run.Type (claim-and-fail-fast) and Loop reports its
	// Message verbatim; any other non-nil error also fails the run, reported via its
	// Error() text (dispatcher-authored, e.g. the frozen k8s job-creation messages, or the
	// actionable decrypt-failure guidance).
	Dispatch(run ClaimedRun) error
}

// UnhandledTypeError is the claim-and-fail-fast signal: a run was claimed for a type
// this dispatcher has no handler/template for. The message is dispatcher-authored
// because the wording differs by runner type (frozen k8s text vs. a more actionable standalone
// message) while the wire shape reported to meshfed does not.
type UnhandledTypeError struct {
	Type    meshapi.RunnerImplementationType
	Message string
}

func (e *UnhandledTypeError) Error() string { return e.Message }

// ClaimOutcome classifies a claim (fetch) failure so Loop knows whether to log it and how
// long to back off before claiming again.
type ClaimOutcome int

const (
	// OutcomeNoRun means nothing was available to claim (e.g. HTTP 404); the normal idle
	// poll outcome -- not logged.
	OutcomeNoRun ClaimOutcome = iota
	// OutcomeNoRunLogged also means nothing was claimed, but the error is unexpected enough
	// to log (e.g. a 409 conflict, or a transport error) -- still just waits for next poll.
	OutcomeNoRunLogged
	// OutcomeBackoff means claiming should pause for LoopConfig.ClaimBackoff before trying
	// again (heir of the tf type's FAILED_WORKER_DELAY).
	OutcomeBackoff
)

// ClaimClassifier turns a claim (fetch) error into a ClaimOutcome. Different runner types have
// different policies for the same underlying error taxonomy
// -- both are pinned and injected, not hardcoded in Loop.
type ClaimClassifier func(error) ClaimOutcome

// Claimer fetches the next available run for this runner/controller identity.
type Claimer interface {
	Claim() (ClaimedRun, error)
}

// StatusApi is the fail-fast backchannel Loop uses to report a run FAILED when no
// dispatcher can handle it, before any handler/Job ever owned the run.
type StatusApi interface {
	RegisterSource(runId RunId) error
	UpdateRunStatus(runId RunId, status, summary, stepMessage string) error
}
