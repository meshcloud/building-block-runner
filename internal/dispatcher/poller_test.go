package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"testing/synctest"
	"time"
	"uuid"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/config/xtime"
)

var runnerUuid = uuid.MustParse("98520496-627d-43e6-82da-ce499179ff3f")

func newManualRun(t *testing.T) Run {
	t.Helper()
	run := Run{Metadata: client.MeshRunnerRunMetadata{Uuid: uuid.New()}}
	run.Spec.RunToken = "token-" + run.Metadata.Uuid.String()
	run.Spec.Definition.Spec.Implementation = []byte(`{"type":"MANUAL"}`)
	return run
}

type fakeApi struct {
	mu        sync.Mutex
	queue     []Run
	claims    int
	failed    map[string]string
	patchErr  error
	claimGate chan struct{}
}

func (a *fakeApi) ClaimRun(ctx context.Context, forRunner uuid.UUID) (client.MeshRunnerRun, error) {
	if a.claimGate != nil {
		<-a.claimGate
	}
	if err := ctx.Err(); err != nil {
		return Run{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if forRunner != runnerUuid {
		return Run{}, errors.New("claimed for another runner")
	}
	a.claims++
	if len(a.queue) == 0 {
		return Run{}, client.ErrNoRunToClaim
	}
	run := a.queue[0]
	a.queue = a.queue[1:]
	return run, nil
}

func (a *fakeApi) ForRun(run client.MeshRunnerRun) client.MeshRunnerRunClient {
	return fakeRunApi{api: a, run: run}
}

func (a *fakeApi) locked(f func()) {
	a.mu.Lock()
	defer a.mu.Unlock()
	f()
}

type fakeRunApi struct {
	client.MeshRunnerRunClient

	api *fakeApi
	run Run
}

func (r fakeRunApi) RegisterSource(ctx context.Context, registration client.MeshRunnerRunSourceRegistration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if registration.Source.Id != runnerUuid {
		return errors.New("registered another source")
	}
	return nil
}

// PatchStatus keys the summary by run token, as every request about a run must use its token.
func (r fakeRunApi) PatchStatus(ctx context.Context, sourceId uuid.UUID, update client.MeshRunnerRunStatusUpdate) (client.MeshRunnerRunStatusUpdateResult, error) {
	if err := ctx.Err(); err != nil {
		return client.MeshRunnerRunStatusUpdateResult{}, err
	}
	if sourceId != runnerUuid || *update.Status != statusFailed {
		return client.MeshRunnerRunStatusUpdateResult{}, errors.New("unexpected status update")
	}
	r.api.mu.Lock()
	defer r.api.mu.Unlock()
	if r.api.patchErr != nil {
		return client.MeshRunnerRunStatusUpdateResult{}, r.api.patchErr
	}
	if r.api.failed == nil {
		r.api.failed = map[string]string{}
	}
	r.api.failed[r.run.Spec.RunToken] = *update.Summary
	return client.MeshRunnerRunStatusUpdateResult{}, nil
}

type fakeDispatcher struct {
	mu        sync.Mutex
	started   []uuid.UUID
	startErr  error
	finished  chan RunFinished
	running   []uuid.UUID
	previous  map[uuid.UUID]Run
	confirmed []uuid.UUID
}

func newFakeDispatcher(previous ...Run) *fakeDispatcher {
	d := &fakeDispatcher{finished: make(chan RunFinished), previous: map[uuid.UUID]Run{}}
	for _, run := range previous {
		d.previous[run.Metadata.Uuid] = run
	}
	return d
}

func (d *fakeDispatcher) Init(context.Context) (InitState, error) {
	return InitState{Running: d.running}, nil
}

func (d *fakeDispatcher) Start(_ context.Context, run Run) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.startErr != nil {
		return d.startErr
	}
	d.started = append(d.started, run.Metadata.Uuid)
	return nil
}

func (d *fakeDispatcher) Finished() <-chan RunFinished {
	return d.finished
}

func (d *fakeDispatcher) ReadRun(_ context.Context, runUuid uuid.UUID) (Run, error) {
	return d.previous[runUuid], nil
}

func (d *fakeDispatcher) FailureReported(_ context.Context, runUuid uuid.UUID) {
	d.confirmed = append(d.confirmed, runUuid)
}

func (d *fakeDispatcher) startedUuids() []uuid.UUID {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.started
}

func testConfig(maxConcurrentJobs int) config.Config {
	var cfg config.Config
	cfg.Uuid = runnerUuid
	cfg.Dispatcher.MaxConcurrentJobs = maxConcurrentJobs
	cfg.Dispatcher.PollingInterval = xtime.Seconds(10)
	return cfg
}

// stop sends the finished runs after the context ended and then closes the channel, as a dispatcher does.
func startPolling(t *testing.T, cfg config.Config, api *fakeApi, dispatcher *fakeDispatcher) (stop func(afterShutdown ...RunFinished) error) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	state, err := dispatcher.Init(ctx)
	require.NoError(t, err)
	p := newPoller(cfg, api, dispatcher, state.Running)
	done := make(chan error)
	go func() { done <- p.poll(ctx) }()
	return func(afterShutdown ...RunFinished) error {
		cancel()
		synctest.Wait()
		for _, finished := range afterShutdown {
			dispatcher.finished <- finished
		}
		close(dispatcher.finished)
		return <-done
	}
}

func Test_Poll_ClaimsUpToCapacityCountingRunsOfAPreviousProcess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		first, second, third := newManualRun(t), newManualRun(t), newManualRun(t)
		previous := uuid.New()
		api := &fakeApi{queue: []Run{first, second, third}}
		dispatcher := newFakeDispatcher()
		dispatcher.running = []uuid.UUID{previous}
		stop := startPolling(t, testConfig(3), api, dispatcher)

		synctest.Sleep(10 * time.Second)
		assert.Equal(t, []uuid.UUID{first.Metadata.Uuid, second.Metadata.Uuid}, dispatcher.startedUuids())

		api.locked(func() { api.claims = 0 })
		synctest.Sleep(10 * time.Second)
		api.locked(func() { assert.Zero(t, api.claims) })

		dispatcher.finished <- RunFinished{RunUuid: previous}
		synctest.Wait()
		assert.Equal(t, []uuid.UUID{first.Metadata.Uuid, second.Metadata.Uuid, third.Metadata.Uuid}, dispatcher.startedUuids())

		require.NoError(t, stop())
	})
}

func Test_Poll_ReportsEachFailureOnceWithTheRunToken(t *testing.T) {
	finished := func(run Run, err error) RunFinished {
		return RunFinished{RunUuid: run.Metadata.Uuid, Err: err}
	}
	claimAll := func(t *testing.T, api *fakeApi, dispatcher *fakeDispatcher) (stop func(afterShutdown ...RunFinished) error) {
		t.Helper()
		stop = startPolling(t, testConfig(len(api.queue)), api, dispatcher)
		synctest.Sleep(10 * time.Second)
		return stop
	}
	finish := func(dispatcher *fakeDispatcher, finished RunFinished) {
		dispatcher.finished <- finished
		synctest.Wait()
	}

	t.Run("a failed run is reported with its run token and the report is confirmed", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			run := newManualRun(t)
			api, dispatcher := &fakeApi{queue: []Run{run}}, newFakeDispatcher()
			stop := claimAll(t, api, dispatcher)

			finish(dispatcher, finished(run, errors.New("OOMKilled")))

			require.NoError(t, stop())
			assert.Equal(t, map[string]string{run.Spec.RunToken: "OOMKilled"}, api.failed)
			assert.Equal(t, []uuid.UUID{run.Metadata.Uuid}, dispatcher.confirmed)
		})
	})

	t.Run("a run that reported its own failure is not reported again", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			run := newManualRun(t)
			api, dispatcher := &fakeApi{queue: []Run{run}}, newFakeDispatcher()
			stop := claimAll(t, api, dispatcher)

			finish(dispatcher, finished(run, fmt.Errorf("no logs dir: %w", ErrFailureReported)))

			require.NoError(t, stop())
			assert.Empty(t, api.failed)
			assert.Empty(t, dispatcher.confirmed)
		})
	})

	t.Run("a failed run of a previous process is reported with the run token the dispatcher reads", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			previous := newManualRun(t)
			api, dispatcher := &fakeApi{}, newFakeDispatcher(previous)
			stop := startPolling(t, testConfig(1), api, dispatcher)

			finish(dispatcher, finished(previous, errors.New("DeadlineExceeded")))

			require.NoError(t, stop())
			assert.Equal(t, map[string]string{previous.Spec.RunToken: "DeadlineExceeded"}, api.failed)
			assert.Equal(t, []uuid.UUID{previous.Metadata.Uuid}, dispatcher.confirmed)
		})
	})

	t.Run("a report that meshStack rejects is not confirmed", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			run := newManualRun(t)
			api, dispatcher := &fakeApi{queue: []Run{run}, patchErr: errors.New("http error 503")}, newFakeDispatcher()
			stop := claimAll(t, api, dispatcher)

			finish(dispatcher, finished(run, errors.New("OOMKilled")))

			require.NoError(t, stop())
			assert.Empty(t, api.failed)
			assert.Empty(t, dispatcher.confirmed)
		})
	})

	t.Run("a run that cannot start is reported", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			run := newManualRun(t)
			api, dispatcher := &fakeApi{queue: []Run{run}}, newFakeDispatcher()
			dispatcher.startErr = errors.New("no executor")

			stop := claimAll(t, api, dispatcher)

			require.NoError(t, stop())
			assert.Equal(t, map[string]string{run.Spec.RunToken: "Failed to start the run: no executor"}, api.failed)
		})
	})

	t.Run("a run that fails after shutdown began is reported", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			run := newManualRun(t)
			api, dispatcher := &fakeApi{queue: []Run{run}}, newFakeDispatcher()
			stop := claimAll(t, api, dispatcher)

			require.NoError(t, stop(finished(run, errors.New("panic"))))

			assert.Equal(t, map[string]string{run.Spec.RunToken: "panic"}, api.failed)
			assert.Equal(t, []uuid.UUID{run.Metadata.Uuid}, dispatcher.confirmed)
		})
	})
}

func Test_Shutdown_LetsAClaimAndItsFailureReportFinish(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		run := newManualRun(t)
		api := &fakeApi{queue: []Run{run}, claimGate: make(chan struct{})}
		dispatcher := newFakeDispatcher()
		dispatcher.startErr = errors.New("no executor")
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error)
		go func() { done <- newPoller(testConfig(1), api, dispatcher, nil).poll(ctx) }()
		synctest.Sleep(10 * time.Second)

		cancel()
		close(api.claimGate)
		synctest.Wait()
		close(dispatcher.finished)

		require.NoError(t, <-done)
		assert.Equal(t, map[string]string{run.Spec.RunToken: "Failed to start the run: no executor"}, api.failed)
	})
}
