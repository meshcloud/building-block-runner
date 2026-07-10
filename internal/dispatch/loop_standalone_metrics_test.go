package dispatch

import (
	"testing"

	"github.com/meshcloud/building-block-runner/internal/meshapi"
)

// fakeStandaloneMetrics records the two additive standalone-persona events Loop drives
// through the optional StandaloneMetrics seam (runner_runs_unhandled_total /
// runner_at_capacity_skips_total).
type fakeStandaloneMetrics struct {
	unhandledTypes []string
	capacitySkips  int
}

func (f *fakeStandaloneMetrics) RunUnhandled(runnerType string) {
	f.unhandledTypes = append(f.unhandledTypes, runnerType)
}
func (f *fakeStandaloneMetrics) AtCapacitySkip() { f.capacitySkips++ }

func newTestLoopWithStandalone(claimer Claimer, dispatcher Dispatcher, statusApi StatusApi, cfg LoopConfig, sm StandaloneMetrics) *Loop {
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 1
	}
	return NewLoop(cfg, LoopDeps{
		RunnerUuid: "runner-uuid",
		Claimer:    claimer,
		Dispatcher: dispatcher,
		StatusApi:  statusApi,
		Classify:   StandaloneClaimClassifier,
		Metrics:    NewMetricsCollector(),
		Standalone: sm,
	})
}

func TestDrainRuns_AtCapacity_FiresStandaloneSkip(t *testing.T) {
	sm := &fakeStandaloneMetrics{}
	// MaxConcurrent 1 with 1 already in flight => available capacity 0 => skip this cycle.
	dispatcher := &fakeDispatcher{inFlight: 1}
	l := newTestLoopWithStandalone(&queueClaimer{}, dispatcher, &fakeStatusApi{}, LoopConfig{MaxConcurrent: 1}, sm)

	l.drainRuns()

	if sm.capacitySkips != 1 {
		t.Errorf("expected 1 at-capacity skip, got %d", sm.capacitySkips)
	}
}

func TestProcessNextRun_UnhandledType_FiresStandaloneUnhandled(t *testing.T) {
	sm := &fakeStandaloneMetrics{}
	run := buildClaimedRun(t, "run-uuid-unhandled")
	claimer := &queueClaimer{queue: []ClaimedRun{run}}
	dispatcher := &fakeDispatcher{dispatchErr: &UnhandledTypeError{
		Type:    meshapi.RunnerTypeTerraform,
		Message: "no handler configured for type 'TERRAFORM'",
	}}
	l := newTestLoopWithStandalone(claimer, dispatcher, &fakeStatusApi{}, LoopConfig{MaxConcurrent: 10}, sm)

	if got := l.processNextRun(); got != processFailed {
		t.Errorf("expected processFailed, got %v", got)
	}
	if len(sm.unhandledTypes) != 1 || sm.unhandledTypes[0] != "TERRAFORM" {
		t.Errorf("expected one TERRAFORM unhandled event, got %v", sm.unhandledTypes)
	}
}

// A nil Standalone (the run-controller persona) must not panic at either hook site.
func TestLoop_NilStandaloneMetrics_NoPanic(t *testing.T) {
	dispatcher := &fakeDispatcher{inFlight: 1}
	l := newTestLoop(&queueClaimer{}, dispatcher, &fakeStatusApi{}, LoopConfig{MaxConcurrent: 1})
	l.drainRuns() // at-capacity path with deps.Standalone == nil
}
