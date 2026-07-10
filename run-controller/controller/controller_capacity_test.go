package controller

import (
	"errors"
	"testing"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
)

// fakeJobManager is a test double for JobManager that records job creations and reports a
// configurable active-job count.
type fakeJobManager struct {
	createCalls int
	createErr   error

	activeJobs int
	countErr   error
}

func (f *fakeJobManager) CreateRunnerJob(_ meshapi.RunInfo, _ string, _ string, _ *JobSpecTemplate, _ *MetricsCollector) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.createCalls++
	f.activeJobs++ // a freshly created job counts towards the active total
	return nil
}

func (f *fakeJobManager) CountActiveJobs() (int, error) {
	if f.countErr != nil {
		return 0, f.countErr
	}
	return f.activeJobs, nil
}

// queueRunApi serves a fixed queue of runs and then returns a 404 (no run available), like the
// real API draining a backlog.
type queueRunApi struct {
	mockRunApi
	queue []struct {
		raw string
		dto *meshapi.RunDetailsDTO
	}
	idx int
}

func (q *queueRunApi) FetchRunDetails(string) (string, *meshapi.RunDetailsDTO, error) {
	if q.idx >= len(q.queue) {
		return "", nil, meshapi.HttpError{StatusCode: 404}
	}
	item := q.queue[q.idx]
	q.idx++
	return item.raw, item.dto, nil
}

// enqueueRuns appends n TERRAFORM runs to the queue.
func (q *queueRunApi) enqueueRuns(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		dto, raw, err := buildRunDetailsWithImplType("TERRAFORM")
		if err != nil {
			t.Fatalf("failed to build run details: %v", err)
		}
		q.queue = append(q.queue, struct {
			raw string
			dto *meshapi.RunDetailsDTO
		}{raw: raw, dto: dto})
	}
}

func TestDrainRuns_ProcessesBacklogBackToBack(t *testing.T) {
	api := &queueRunApi{}
	api.enqueueRuns(t, 3)

	ctrl, cleanup := setupControllerWithMockApi(&api.mockRunApi, map[string]JobSpecTemplate{
		"TERRAFORM": {Image: "tf:latest"},
	})
	defer cleanup()
	ctrl.runApi = api
	AppConfig.MaxConcurrentJobs = 10
	fake := &fakeJobManager{}
	ctrl.k8sClient = fake

	ctrl.drainRuns()

	// All 3 queued runs should be drained in a single cycle, then it stops on the 404.
	if fake.createCalls != 3 {
		t.Errorf("expected 3 jobs created in one drain cycle, got %d", fake.createCalls)
	}
}

func TestDrainRuns_StopsAtCapacity(t *testing.T) {
	api := &queueRunApi{}
	api.enqueueRuns(t, 5)

	ctrl, cleanup := setupControllerWithMockApi(&api.mockRunApi, map[string]JobSpecTemplate{
		"TERRAFORM": {Image: "tf:latest"},
	})
	defer cleanup()
	ctrl.runApi = api
	AppConfig.MaxConcurrentJobs = 3
	fake := &fakeJobManager{activeJobs: 1} // 1 job already running -> only 2 slots free
	ctrl.k8sClient = fake

	ctrl.drainRuns()

	if fake.createCalls != 2 {
		t.Errorf("expected 2 jobs created (capacity 3 minus 1 active), got %d", fake.createCalls)
	}
	// Two runs were consumed from the queue; the rest are left for a later cycle.
	if api.idx != 2 {
		t.Errorf("expected only 2 runs claimed from the API, got %d", api.idx)
	}
}

func TestDrainRuns_SkipsWhenAlreadyAtCapacity(t *testing.T) {
	api := &queueRunApi{}
	api.enqueueRuns(t, 2)

	ctrl, cleanup := setupControllerWithMockApi(&api.mockRunApi, map[string]JobSpecTemplate{
		"TERRAFORM": {Image: "tf:latest"},
	})
	defer cleanup()
	ctrl.runApi = api
	AppConfig.MaxConcurrentJobs = 3
	fake := &fakeJobManager{activeJobs: 3} // already at the limit
	ctrl.k8sClient = fake

	ctrl.drainRuns()

	if fake.createCalls != 0 {
		t.Errorf("expected no jobs created when at capacity, got %d", fake.createCalls)
	}
	// No run should be claimed from the API when we know we cannot place it.
	if api.idx != 0 {
		t.Errorf("expected no runs claimed when at capacity, got %d", api.idx)
	}
}

func TestDrainRuns_StopsOnProcessFailure(t *testing.T) {
	api := &queueRunApi{}
	api.enqueueRuns(t, 3)

	ctrl, cleanup := setupControllerWithMockApi(&api.mockRunApi, map[string]JobSpecTemplate{
		"TERRAFORM": {Image: "tf:latest"},
	})
	defer cleanup()
	ctrl.runApi = api
	AppConfig.MaxConcurrentJobs = 10
	fake := &fakeJobManager{createErr: errors.New("quota exceeded")}
	ctrl.k8sClient = fake

	ctrl.drainRuns()

	// First run fails job creation -> reported FAILED and draining stops for this cycle.
	if api.idx != 1 {
		t.Errorf("expected draining to stop after the first failure, claimed %d runs", api.idx)
	}
	if api.updatedStatus != "FAILED" {
		t.Errorf("expected the failed run to be reported FAILED, got %q", api.updatedStatus)
	}
}

func TestAvailableCapacity(t *testing.T) {
	ctrl, cleanup := setupControllerWithMockApi(&mockRunApi{}, map[string]JobSpecTemplate{})
	defer cleanup()

	t.Run("partial capacity", func(t *testing.T) {
		AppConfig.MaxConcurrentJobs = 10
		ctrl.k8sClient = &fakeJobManager{activeJobs: 4}
		if got := ctrl.availableCapacity(); got != 6 {
			t.Errorf("expected 6 available, got %d", got)
		}
	})

	t.Run("at capacity returns zero", func(t *testing.T) {
		AppConfig.MaxConcurrentJobs = 5
		ctrl.k8sClient = &fakeJobManager{activeJobs: 5}
		if got := ctrl.availableCapacity(); got != 0 {
			t.Errorf("expected 0 available, got %d", got)
		}
	})

	t.Run("over capacity returns zero, never negative", func(t *testing.T) {
		AppConfig.MaxConcurrentJobs = 5
		ctrl.k8sClient = &fakeJobManager{activeJobs: 8}
		if got := ctrl.availableCapacity(); got != 0 {
			t.Errorf("expected 0 available, got %d", got)
		}
	})

	t.Run("unlimited when negative", func(t *testing.T) {
		AppConfig.MaxConcurrentJobs = -1
		ctrl.k8sClient = &fakeJobManager{activeJobs: 999}
		if got := ctrl.availableCapacity(); got != maxDrainPerCycleUnlimited {
			t.Errorf("expected unlimited (%d), got %d", maxDrainPerCycleUnlimited, got)
		}
	})

	t.Run("count error skips cycle", func(t *testing.T) {
		AppConfig.MaxConcurrentJobs = 10
		ctrl.k8sClient = &fakeJobManager{countErr: errors.New("api down")}
		if got := ctrl.availableCapacity(); got != 0 {
			t.Errorf("expected 0 available on count error, got %d", got)
		}
	})
}

func TestProcessNextRun_SuccessReturnsRunProcessed(t *testing.T) {
	dto, raw, err := buildRunDetailsWithImplType("TERRAFORM")
	if err != nil {
		t.Fatalf("failed to build run details: %v", err)
	}
	mock := &mockRunApi{fetchResult: dto, fetchRawBase64: raw}

	ctrl, cleanup := setupControllerWithMockApi(mock, map[string]JobSpecTemplate{
		"TERRAFORM": {Image: "tf:latest"},
	})
	defer cleanup()
	ctrl.k8sClient = &fakeJobManager{}

	if got := ctrl.processNextRun(); got != runProcessed {
		t.Errorf("expected runProcessed, got %v", got)
	}
}
