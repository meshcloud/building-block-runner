package tf

// meter_test.go pins the generic standalone-runner metrics hooks:
// RunClaimed/RunSucceeded/RunFailed/PollError, driven black-box through the WorkerTestSuite's
// runWorker (Worker.tfExecution, the execution engine the dispatch path invokes per run) rather
// than by calling the unexported hook sites directly.

import (
	"sync"
	"time"
)

// fakeMeter records every event with a mutex (workRoutine/observerRoutine run
// concurrently, so this must be -race clean like the rest of the suite).
type fakeMeter struct {
	mu         sync.Mutex
	claimed    int
	succeeded  int
	failed     int
	pollErrors int
	durations  []time.Duration
}

func (m *fakeMeter) RunClaimed() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.claimed++
}

func (m *fakeMeter) RunSucceeded(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.succeeded++
	m.durations = append(m.durations, d)
}

func (m *fakeMeter) RunFailed(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failed++
	m.durations = append(m.durations, d)
}

func (m *fakeMeter) PollError() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pollErrors++
}

// snapshot avoids the test goroutine racing the worker goroutine on the counter reads
// themselves.
type meterCounts struct {
	claimed, succeeded, failed, pollErrors int
}

func (m *fakeMeter) snapshot() meterCounts {
	m.mu.Lock()
	defer m.mu.Unlock()
	return meterCounts{m.claimed, m.succeeded, m.failed, m.pollErrors}
}
