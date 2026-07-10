package dispatch

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestNewMetricsCollectorWithRegistry_IsolatesRegistries proves the PLAN_DETAIL_03 §5.6
// injectable seam: two collectors built on two fresh registries do not share counters (so a
// test never has to fight the NewMetricsCollector sync.Once), and each registry actually
// carries the frozen run_controller_* series -- exercised end-to-end via Gather so the
// registration (not just the struct wiring) is asserted.
func TestNewMetricsCollectorWithRegistry_IsolatesRegistries(t *testing.T) {
	regA := prometheus.NewRegistry()
	regB := prometheus.NewRegistry()
	a := NewMetricsCollectorWithRegistry(regA)
	b := NewMetricsCollectorWithRegistry(regB)

	a.IncRunsFetchError("uuid-a", ErrorTypeFetchAPI)

	if got := testutil.ToFloat64(a.runsFetchErrors.WithLabelValues("uuid-a", ErrorTypeFetchAPI)); got != 1 {
		t.Errorf("collector A: expected 1, got %v", got)
	}
	// The isolation guarantee: incrementing A must not bleed into B's independent registry.
	if got := testutil.ToFloat64(b.runsFetchErrors.WithLabelValues("uuid-a", ErrorTypeFetchAPI)); got != 0 {
		t.Errorf("collector B leaked A's increment: expected 0, got %v", got)
	}

	mfs, err := regA.Gather()
	if err != nil {
		t.Fatalf("gather regA: %v", err)
	}
	var found bool
	for _, mf := range mfs {
		if mf.GetName() == "run_controller_runs_fetch_errors_total" {
			found = true
		}
	}
	if !found {
		t.Error("run_controller_runs_fetch_errors_total not registered on the injected registry")
	}
}

// TestMetricsCollector_ExportedMethodsIncrementTheRightSeries exercises every exported
// method on the frozen run_controller_* series (D12) -- this is the surface internal/k8sjob
// and the cmd/bbrunner wiring drive these metrics through without importing prometheus
// themselves (structural JobMetrics/StatusApi-style satisfaction).
func TestMetricsCollector_ExportedMethodsIncrementTheRightSeries(t *testing.T) {
	m := NewMetricsCollector()

	m.IncRunsFetchError("uuid-1", ErrorTypeFetchAPI)
	if got := testutil.ToFloat64(m.runsFetchErrors.WithLabelValues("uuid-1", ErrorTypeFetchAPI)); got != 1 {
		t.Errorf("IncRunsFetchError: expected 1, got %v", got)
	}

	m.ObserveRunsFetchDuration("uuid-1", 0.5)
	if got := testutil.CollectAndCount(m.runsFetchDuration); got == 0 {
		t.Errorf("ObserveRunsFetchDuration: expected at least one observation")
	}

	m.IncJobsCreated("uuid-1")
	if got := testutil.ToFloat64(m.jobsCreatedTotal.WithLabelValues("uuid-1")); got != 1 {
		t.Errorf("IncJobsCreated: expected 1, got %v", got)
	}

	m.IncJobCreationError("uuid-1", ErrorTypeRunTooLarge)
	if got := testutil.ToFloat64(m.jobCreationErrors.WithLabelValues("uuid-1", ErrorTypeRunTooLarge)); got != 1 {
		t.Errorf("IncJobCreationError: expected 1, got %v", got)
	}

	m.ObserveJobCreationDuration("uuid-1", 0.25)
	if got := testutil.CollectAndCount(m.jobCreationDuration); got == 0 {
		t.Errorf("ObserveJobCreationDuration: expected at least one observation")
	}

	m.IncJobsAtCapacitySkips("uuid-1")
	if got := testutil.ToFloat64(m.jobsAtCapacitySkips.WithLabelValues("uuid-1")); got != 1 {
		t.Errorf("IncJobsAtCapacitySkips: expected 1, got %v", got)
	}

	m.IncServiceAccountsCreated("uuid-1")
	if got := testutil.ToFloat64(m.serviceAccountsCreatedTotal.WithLabelValues("uuid-1")); got != 1 {
		t.Errorf("IncServiceAccountsCreated: expected 1, got %v", got)
	}

	m.IncServiceAccountCreationError("uuid-1", ErrorTypeServiceAccountCreation)
	if got := testutil.ToFloat64(m.serviceAccountCreationErrors.WithLabelValues("uuid-1", ErrorTypeServiceAccountCreation)); got != 1 {
		t.Errorf("IncServiceAccountCreationError: expected 1, got %v", got)
	}

	m.IncDecryptionError("uuid-1")
	if got := testutil.ToFloat64(m.decryptionErrors.WithLabelValues("uuid-1")); got != 1 {
		t.Errorf("IncDecryptionError: expected 1, got %v", got)
	}

	m.IncRegistrationSuccess("uuid-1")
	if got := testutil.ToFloat64(m.runnerRegistrationSuccess.WithLabelValues("uuid-1")); got != 1 {
		t.Errorf("IncRegistrationSuccess: expected 1, got %v", got)
	}

	m.IncRegistrationError("uuid-1", ErrorTypeRegistrationPut)
	if got := testutil.ToFloat64(m.runnerRegistrationErrors.WithLabelValues("uuid-1", ErrorTypeRegistrationPut)); got != 1 {
		t.Errorf("IncRegistrationError: expected 1, got %v", got)
	}

	before := testutil.ToFloat64(m.controllerLoopIterations)
	m.IncLoopIteration()
	if got := testutil.ToFloat64(m.controllerLoopIterations); got != before+1 {
		t.Errorf("IncLoopIteration: expected %v, got %v", before+1, got)
	}

	m.SetActiveRunners(3)
	if got := testutil.ToFloat64(m.activeRunners); got != 3 {
		t.Errorf("SetActiveRunners: expected 3, got %v", got)
	}
}
