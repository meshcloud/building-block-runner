package tf

import "time"

// Meter is the tf dispatch path's consumer-side seam for the generic standalone-
// runner metrics: defined here (where it is consumed) rather than in internal/observability, so
// this package never needs to import prometheus/* -- *observability.RunMetrics satisfies it
// structurally, and tests use a fake. Single-run stays metrics-free (the metrics are hooked
// into the dispatch loop / Handler.Execute, not the execution engine); only the Handler and
// Worker call these.
type Meter interface {
	// RunClaimed is called once a run has been successfully fetched (claimed).
	RunClaimed()
	// RunSucceeded is called once a claimed run reaches a successful terminal status,
	// with the duration from claim to that terminal status.
	RunSucceeded(d time.Duration)
	// RunFailed is called once a claimed run reaches a failed terminal status, with the
	// duration from claim to that terminal status.
	RunFailed(d time.Duration)
	// PollError is called on a non-norun claim error (i.e. neither "no run available" nor
	// the benign double-chunked-transport-encoding quirk, see NewClaimClassifier).
	PollError()
}

// NoopMeter discards every event -- the zero value a Worker falls back to when no Meter was
// supplied (e.g. the existing scenario-test Worker literals that predate this feature), so
// metrics remain strictly additive.
type NoopMeter struct{}

func (NoopMeter) RunClaimed()                {}
func (NoopMeter) RunSucceeded(time.Duration) {}
func (NoopMeter) RunFailed(time.Duration)    {}
func (NoopMeter) PollError()                 {}
