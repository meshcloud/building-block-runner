package tf

import "sync"

// progress is the mutex-guarded published run status. It replaces the former
// RunContextInfo.reportStatus shallow copy that shared its Steps slice with the live runStatus,
// which raced the observer goroutine's marshal against the work goroutine's mutations (B10).
// The work goroutine publishes immutable snapshots; the observer reads independent copies.
type progress struct {
	mu       sync.Mutex
	snapshot RunStatus
}

func newProgress(initial RunStatus) *progress {
	return &progress{snapshot: initial.clone()}
}

// publish replaces the snapshot with an independent copy of s (work-goroutine write).
func (p *progress) publish(s RunStatus) {
	p.mu.Lock()
	p.snapshot = s.clone()
	p.mu.Unlock()
}

// setStatus overrides only the published status, without republishing the live run status.
// This preserves the register-failure path where FAILED is reported with the steps as they were
// last published (nil before any commit).
func (p *progress) setStatus(s ExecutionStatus) {
	p.mu.Lock()
	p.snapshot.Status = s
	p.mu.Unlock()
}

// Snapshot returns an independent copy of the published status (observer-goroutine read).
func (p *progress) Snapshot() RunStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.snapshot.clone()
}
