package azdevops

import "time"

// Clock abstracts both the poller's 10s wait and its 30-min deadline (§4.4) so tests never
// sleep: a fake Clock jumps Now() and fires After() synchronously. One Clock governs both
// concerns, matching Kotlin's single injected java.time.Clock parameter
// (AzureDevOpsPipelinePoller.kt:24) even though Kotlin only used it for the timeout check
// (Thread.sleep itself was not injectable there, §3.2/§16.13) -- the Go port makes the wait
// itself injectable too, which is what makes the Go twins sleep-free.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// RealClock is the production Clock: real wall-clock time, real timers.
type RealClock struct{}

func (RealClock) Now() time.Time                         { return time.Now() }
func (RealClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
