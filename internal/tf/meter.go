package tf

import "time"

// Meter is the tf polling loop's consumer-side seam for the D12 generic standalone-
// runner metrics (PLAN_HIGH_LEVEL.md D12, PLAN_DETAIL_04 §4.3): defined here (where it
// is consumed, P3) rather than in internal/mgmt, so this package never needs to import
// prometheus/* -- *mgmt.RunMetrics satisfies it structurally, and tests use a fake.
// Single-run stays metrics-free (§4.3's "hooked into the polling loop, not the engine");
// only DefaultRunManager/Worker call these.
type Meter interface {
	// RunClaimed is called once a run has been successfully fetched (claimed).
	RunClaimed()
	// RunSucceeded is called once a claimed run reaches a successful terminal status,
	// with the duration from claim to that terminal status.
	RunSucceeded(d time.Duration)
	// RunFailed is called once a claimed run reaches a failed terminal status, with the
	// duration from claim to that terminal status.
	RunFailed(d time.Duration)
	// PollError is called on a non-norun fetch error (i.e. neither "no run available"
	// nor the benign double-chunked-transport-encoding quirk, see handleFetchRunError).
	PollError()
}

// NoopMeter discards every event -- the zero value a Worker/DefaultRunManager falls
// back to when no Meter was supplied (e.g. the existing scenario-test Worker literals
// that predate this feature), so metrics remain strictly additive.
type NoopMeter struct{}

func (NoopMeter) RunClaimed()                {}
func (NoopMeter) RunSucceeded(time.Duration) {}
func (NoopMeter) RunFailed(time.Duration)    {}
func (NoopMeter) PollError()                 {}
