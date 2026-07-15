package github

import (
	"context"
	"time"
)

// Clock is the single injected time seam used for BOTH the JWT claims (iat/exp) and the
// find/poll waits. The Kotlin code mints the JWT from an unclocked Instant.now() while
// polling uses an injected clock; the Go port deliberately routes both through one Clock
// so tests are fully deterministic.
type Clock interface {
	// Now returns the current time (JWT iat/exp base and poll timeout/find-window base).
	Now() time.Time
	// Wait blocks for d, or until ctx is cancelled — whichever comes first. It reports
	// whether the full duration elapsed (true) or ctx cancelled it first (false).
	Wait(ctx context.Context, d time.Duration) bool
}

// RealClock is the production Clock: wall time and a real cancelable timer.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

func (RealClock) Wait(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
