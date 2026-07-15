package main

import "time"

// successMeter is tf.Meter's single-run-mode implementation: it only tracks whether
// RunSucceeded ever fired (a real terminal SUCCEEDED status), which executeSingleRun reports
// back to runmode.SingleRunResultFromFile / observability.InstrumentSingleRunResult as the
// success signal for its own metering and push-gateway push.
type successMeter struct {
	succeeded bool
}

func (s *successMeter) RunClaimed()                {}
func (s *successMeter) RunSucceeded(time.Duration) { s.succeeded = true }
func (s *successMeter) RunFailed(time.Duration)    {}
func (s *successMeter) PollError()                 {}
