package controller

import (
	"errors"
	"testing"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
)

type fakeJobManager struct {
	createCalls int
	lastImage   string
}

func (f *fakeJobManager) CreateRunnerJob(_ meshapi.RunInfo, _ string, _ string, jobSpec *JobSpecTemplate, _ *MetricsCollector) error {
	f.createCalls++
	f.lastImage = jobSpec.Image
	return nil
}

func (f *fakeJobManager) CountActiveJobs() (int, error) {
	return 0, nil
}

type fakeDispatcher struct {
	dispatchCalls int
	dispatchErr   error

	active   int
	countErr error
}

func (f *fakeDispatcher) Dispatch(*meshapi.RunDetailsDTO, string, string) error {
	if f.dispatchErr != nil {
		return f.dispatchErr
	}
	f.dispatchCalls++
	f.active++
	return nil
}

func (f *fakeDispatcher) CountActive() (int, error) {
	if f.countErr != nil {
		return 0, f.countErr
	}
	return f.active, nil
}

// queueRunApi answers 404 once its queue is empty, like the API after a drained backlog.
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
		return "", nil, &meshapi.StatusError{Status: 404}
	}
	item := q.queue[q.idx]
	q.idx++
	return item.raw, item.dto, nil
}

func (q *queueRunApi) enqueueRuns(t *testing.T, n int) {
	t.Helper()
	for range n {
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
	fake := &fakeDispatcher{}
	ctrl.dispatcher = fake

	ctrl.drainRuns()

	if fake.dispatchCalls != 3 {
		t.Errorf("expected 3 jobs created in one drain cycle, got %d", fake.dispatchCalls)
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
	fake := &fakeDispatcher{active: 1}
	ctrl.dispatcher = fake

	ctrl.drainRuns()

	if fake.dispatchCalls != 2 {
		t.Errorf("expected 2 jobs created (capacity 3 minus 1 active), got %d", fake.dispatchCalls)
	}
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
	fake := &fakeDispatcher{active: 3}
	ctrl.dispatcher = fake

	ctrl.drainRuns()

	if fake.dispatchCalls != 0 {
		t.Errorf("expected no jobs created when at capacity, got %d", fake.dispatchCalls)
	}
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
	fake := &fakeDispatcher{dispatchErr: errors.New("quota exceeded")}
	ctrl.dispatcher = fake

	ctrl.drainRuns()

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
		ctrl.dispatcher = &fakeDispatcher{active: 4}
		if got := ctrl.availableCapacity(); got != 6 {
			t.Errorf("expected 6 available, got %d", got)
		}
	})

	t.Run("at capacity returns zero", func(t *testing.T) {
		AppConfig.MaxConcurrentJobs = 5
		ctrl.dispatcher = &fakeDispatcher{active: 5}
		if got := ctrl.availableCapacity(); got != 0 {
			t.Errorf("expected 0 available, got %d", got)
		}
	})

	t.Run("over capacity returns zero, never negative", func(t *testing.T) {
		AppConfig.MaxConcurrentJobs = 5
		ctrl.dispatcher = &fakeDispatcher{active: 8}
		if got := ctrl.availableCapacity(); got != 0 {
			t.Errorf("expected 0 available, got %d", got)
		}
	})

	t.Run("unlimited when negative", func(t *testing.T) {
		AppConfig.MaxConcurrentJobs = -1
		ctrl.dispatcher = &fakeDispatcher{active: 999}
		if got := ctrl.availableCapacity(); got != maxDrainPerCycleUnlimited {
			t.Errorf("expected unlimited (%d), got %d", maxDrainPerCycleUnlimited, got)
		}
	})

	t.Run("count error skips cycle", func(t *testing.T) {
		AppConfig.MaxConcurrentJobs = 10
		ctrl.dispatcher = &fakeDispatcher{countErr: errors.New("api down")}
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
	ctrl.dispatcher = &fakeDispatcher{}

	if got := ctrl.processNextRun(); got != runProcessed {
		t.Errorf("expected runProcessed, got %v", got)
	}
}
