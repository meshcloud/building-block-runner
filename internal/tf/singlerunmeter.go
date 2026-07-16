package tf

import "time"

// SuccessMeter is Meter's single-run-mode implementation: it only tracks whether
// RunSucceeded ever fired (a real terminal SUCCEEDED status), which the single-run bootstrap
// reports back to runmode.SingleRunResultFromFile / observability.InstrumentSingleRunResult as
// the success signal for its own metering and push-gateway push.
type SuccessMeter struct {
	Succeeded bool
}

func (s *SuccessMeter) RunClaimed()                {}
func (s *SuccessMeter) RunSucceeded(time.Duration) { s.Succeeded = true }
func (s *SuccessMeter) RunFailed(time.Duration)    {}
func (s *SuccessMeter) PollError()                 {}
