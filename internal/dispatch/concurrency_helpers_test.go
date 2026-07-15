package dispatch_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
)

// This file holds small shared fixtures for the concurrency-hazard suite: one named test
// per hazard lives in its own sibling _test.go file; only the generic plumbing they all
// need lives here.

// concurrencyTestHandlerFunc adapts a plain function to dispatch.RunHandler, so each
// hazard test can express its handler inline instead of declaring a one-off named type.
type concurrencyTestHandlerFunc func(ctx context.Context, run dispatch.ClaimedRun) error

func (f concurrencyTestHandlerFunc) Execute(ctx context.Context, run dispatch.ClaimedRun) error {
	return f(ctx, run)
}

// discardLogger is a *slog.Logger that throws every record away -- these tests assert on
// captured requests/handler-recorded state, not on log output (the log-isolation test is
// the one exception and builds its own logger explicitly).
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// hazardQueueClaimer hands out a fixed queue of ClaimedRun values (FIFO), one per Claim() call,
// then returns errNoRunQueued forever -- a minimal fake Claimer for driving dispatch.Loop
// in tests without a real meshfed server. It records how many runs it has handed out so
// tests can assert on the loop's claim cadence.
type hazardQueueClaimer struct {
	mu     chan struct{} // 1-buffered mutex substitute avoiding an extra import
	queue  []dispatch.ClaimedRun
	served int
}

func newHazardQueueClaimer(runs ...dispatch.ClaimedRun) *hazardQueueClaimer {
	c := &hazardQueueClaimer{mu: make(chan struct{}, 1), queue: runs}
	c.mu <- struct{}{}
	return c
}

// errNoRunQueued is what hazardQueueClaimer returns once its queue is drained; classify it as
// dispatch.OutcomeNoRun in tests, mirroring the real "no run available" claim outcome.
type errNoRunQueued struct{}

func (errNoRunQueued) Error() string { return "hazardQueueClaimer: no run queued" }

func (c *hazardQueueClaimer) Claim() (dispatch.ClaimedRun, error) {
	<-c.mu
	defer func() { c.mu <- struct{}{} }()

	if len(c.queue) == 0 {
		return dispatch.ClaimedRun{}, errNoRunQueued{}
	}
	run := c.queue[0]
	c.queue = c.queue[1:]
	c.served++
	return run, nil
}

func (c *hazardQueueClaimer) Served() int {
	<-c.mu
	defer func() { c.mu <- struct{}{} }()
	return c.served
}

// alwaysNoRun is a dispatch.ClaimClassifier for tests whose Claimer only ever produces
// errNoRunQueued (or an equivalent idle signal) -- every claim error is the ordinary idle
// poll outcome, never logged, never backed off.
func alwaysNoRun(error) dispatch.ClaimOutcome { return dispatch.OutcomeNoRun }

// noopStatusApi is a dispatch.StatusApi that never fails -- used by tests whose fail-fast
// path is not itself under test.
type noopStatusApi struct{}

func (noopStatusApi) RegisterSource(dispatch.RunId) error                          { return nil }
func (noopStatusApi) UpdateRunStatus(dispatch.RunId, string, string, string) error { return nil }

// zeroDispatcher is a Dispatcher that reports zero in-flight and is never actually expected
// to be dispatched to (used by tests whose Claimer never yields a run) -- it exists purely
// to satisfy LoopDeps.Dispatcher.
type zeroDispatcher struct{}

func (zeroDispatcher) InFlight() (int, error)             { return 0, nil }
func (zeroDispatcher) Dispatch(dispatch.ClaimedRun) error { return nil }

// newClaimedRun builds a minimal ClaimedRun for tests that only need identity/type/runToken
// -- the concurrency hazards this suite targets do not exercise the full DTO shape. Type is
// set directly (for tests that Dispatch straight to a dispatcher) AND the Definition's
// Implementation JSON carries the matching "type" (for tests driven through dispatch.Loop,
// which re-resolves Type from Details via GetImplementationType/ToRunnerType and ignores
// whatever the caller put in the Type field, loop.go's processNextRun).
func newClaimedRun(id string, runToken string) dispatch.ClaimedRun {
	return dispatch.ClaimedRun{
		Id:   dispatch.RunId(id),
		Type: meshapi.RunnerTypeTerraform,
		Details: &meshapi.RunDetailsDTO{
			Metadata: meshapi.RunMetaDTO{Uuid: id},
			Spec: meshapi.RunSpecDTO{
				RunToken: runToken,
				Definition: meshapi.DefinitionSpecDTO{
					Spec: meshapi.DefinitionDetailsSpecDTO{
						Implementation: json.RawMessage(`{"type":"TERRAFORM"}`),
					},
				},
			},
		},
	}
}
