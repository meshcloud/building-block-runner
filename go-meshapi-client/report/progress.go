package report

import "sync"

// Progress is the mutex-guarded published run status shared between the goroutine that
// executes a run and the goroutine(s) that report its status. Every read/write goes through a
// Clone, so the two sides never share a backing array to race over (B10 fix, plan 02 §5.5 —
// the shape this phase relocates unchanged, generalized from the tf-only predecessor).
type Progress struct {
	mu       sync.Mutex
	snapshot RunStatus
}

// NewProgress creates a Progress published with an independent copy of initial.
func NewProgress(initial RunStatus) *Progress {
	return &Progress{snapshot: initial.Clone()}
}

// Mutate applies f to a private working copy of the published status and publishes the result
// atomically. f may freely mutate the RunStatus it receives (e.g. reassign Steps, flip Status) —
// it never observes or affects any other goroutine's in-flight Snapshot.
func (p *Progress) Mutate(f func(*RunStatus)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	working := p.snapshot.Clone()
	f(&working)
	p.snapshot = working
}

// Snapshot returns an independent copy of the currently published status.
func (p *Progress) Snapshot() RunStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.snapshot.Clone()
}
