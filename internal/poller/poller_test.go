package poller

import (
	"context"
	"errors"
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
	"github.com/meshcloud/building-block-runner/internal/dispatcher"
	"github.com/meshcloud/building-block-runner/internal/executor"
)

var runnerUuid = uuid.MustParse("98520496-627d-43e6-82da-ce499179ff3f")

func newManualRun(t *testing.T) dispatcher.Run {
	t.Helper()
	run := dispatcher.Run{Metadata: client.MeshRunnerRunMetadata{Uuid: uuid.New()}}
	run.Spec.RunToken = "token-" + run.Metadata.Uuid.String()
	run.Spec.Definition.Spec.Implementation = client.MeshRunnerRunImplementation{Type: client.MeshRunnerImplementationTypeManual, Manual: &client.MeshRunnerManualImplementation{}}
	return run
}

type fakeApi struct {
	client.MeshRunnerClient

	mu        sync.Mutex
	queue     []dispatcher.Run
	claims    int
	failed    map[uuid.UUID]string
	claimGate chan struct{}
}

func (a *fakeApi) ClaimRun(ctx context.Context, forRunner uuid.UUID) (client.MeshRunnerRun, error) {
	if a.claimGate != nil {
		<-a.claimGate
	}
	if err := ctx.Err(); err != nil {
		return dispatcher.Run{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if forRunner != runnerUuid {
		return dispatcher.Run{}, errors.New("claimed for another runner")
	}
	a.claims++
	if len(a.queue) == 0 {
		return dispatcher.Run{}, client.ErrNoRunToClaim
	}
	run := a.queue[0]
	a.queue = a.queue[1:]
	return run, nil
}

func (a *fakeApi) ForRunUuid(runUuid uuid.UUID) client.MeshRunnerRunClient {
	return fakeRunApi{api: a, runUuid: runUuid}
}

func (a *fakeApi) locked(f func()) {
	a.mu.Lock()
	defer a.mu.Unlock()
	f()
}

type fakeRunApi struct {
	client.MeshRunnerRunClient

	api     *fakeApi
	runUuid uuid.UUID
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

func (r fakeRunApi) PatchStatus(ctx context.Context, sourceId uuid.UUID, update client.MeshRunnerRunStatusUpdate) (client.MeshRunnerRunStatusUpdateResult, error) {
	if err := ctx.Err(); err != nil {
		return client.MeshRunnerRunStatusUpdateResult{}, err
	}
	if sourceId != runnerUuid || *update.Status != string(executor.StatusFailed) {
		return client.MeshRunnerRunStatusUpdateResult{}, errors.New("unexpected status update")
	}
	r.api.mu.Lock()
	defer r.api.mu.Unlock()
	if r.api.failed == nil {
		r.api.failed = map[uuid.UUID]string{}
	}
	r.api.failed[r.runUuid] = *update.Steps[0].UserMessage
	return client.MeshRunnerRunStatusUpdateResult{}, nil
}

type fakeDispatcher struct {
	mu       sync.Mutex
	started  []uuid.UUID
	startErr error
	finished chan dispatcher.Finished
	running  []uuid.UUID
}

func newFakeDispatcher() *fakeDispatcher {
	return &fakeDispatcher{finished: make(chan dispatcher.Finished)}
}

func (d *fakeDispatcher) Init(context.Context) (dispatcher.InitState, error) {
	return dispatcher.InitState{Running: d.running}, nil
}

func (d *fakeDispatcher) Dispatch(_ context.Context, run dispatcher.Run, _ string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.startErr != nil {
		return d.startErr
	}
	d.started = append(d.started, run.Metadata.Uuid)
	return nil
}

func (d *fakeDispatcher) Finished() <-chan dispatcher.Finished {
	return d.finished
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
func startPolling(t *testing.T, cfg config.Config, api *fakeApi, runs *fakeDispatcher) (stop func(afterShutdown ...dispatcher.Finished) error) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	state, err := runs.Init(ctx)
	require.NoError(t, err)
	p := newPoller(cfg, api, runs, func() {}, state.Running, nil)
	done := make(chan error)
	go func() { done <- p.poll(ctx) }()
	return func(afterShutdown ...dispatcher.Finished) error {
		cancel()
		synctest.Wait()
		for _, finished := range afterShutdown {
			runs.finished <- finished
		}
		close(runs.finished)
		return <-done
	}
}

func Test_Poll_ClaimsUpToCapacityCountingRunsOfAPreviousProcess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		first, second, third := newManualRun(t), newManualRun(t), newManualRun(t)
		previous := uuid.New()
		api := &fakeApi{queue: []dispatcher.Run{first, second, third}}
		runs := newFakeDispatcher()
		runs.running = []uuid.UUID{previous}
		stop := startPolling(t, testConfig(3), api, runs)

		synctest.Sleep(10 * time.Second)
		assert.Equal(t, []uuid.UUID{first.Metadata.Uuid, second.Metadata.Uuid}, runs.startedUuids())

		api.locked(func() { api.claims = 0 })
		synctest.Sleep(10 * time.Second)
		api.locked(func() { assert.Zero(t, api.claims) })

		runs.finished <- dispatcher.Finished{RunUuid: previous}
		synctest.Wait()
		assert.Equal(t, []uuid.UUID{first.Metadata.Uuid, second.Metadata.Uuid, third.Metadata.Uuid}, runs.startedUuids())

		require.NoError(t, stop())
	})
}

type confirmations []uuid.UUID

func (c *confirmations) finished(run dispatcher.Run, exitCode int, failure string) dispatcher.Finished {
	return dispatcher.Finished{
		RunUuid:  run.Metadata.Uuid,
		ExitCode: exitCode,
		Failure:  failure,
		Reported: func(context.Context) { *c = append(*c, run.Metadata.Uuid) },
	}
}

func Test_Poll_ReportsAFailureOnlyForAnUndeliveredExitCode(t *testing.T) {
	claimAll := func(t *testing.T, api *fakeApi, runs *fakeDispatcher) (stop func(afterShutdown ...dispatcher.Finished) error) {
		t.Helper()
		stop = startPolling(t, testConfig(len(api.queue)), api, runs)
		synctest.Sleep(10 * time.Second)
		return stop
	}
	finish := func(runs *fakeDispatcher, finished dispatcher.Finished) {
		runs.finished <- finished
		synctest.Wait()
	}

	for name, test := range map[string]struct {
		exitCode int
		failure  string
		reported string
	}{
		"exit 0 delivered the status":           {exitCode: executor.ExitStatusDelivered},
		"exit 3 delivered the failure":          {exitCode: executor.ExitFailedDelivered},
		"exit 1 is reported with the failure":   {exitCode: executor.ExitUndelivered, failure: "OOMKilled", reported: "OOMKilled"},
		"a kill is reported with its exit code": {exitCode: -1, reported: "The run ended with exit code -1 before it reported its status."},
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var confirmed confirmations
				run := newManualRun(t)
				api, runs := &fakeApi{queue: []dispatcher.Run{run}}, newFakeDispatcher()
				stop := claimAll(t, api, runs)

				finish(runs, confirmed.finished(run, test.exitCode, test.failure))

				require.NoError(t, stop())
				if test.reported == "" {
					assert.Empty(t, api.failed)
					assert.Empty(t, confirmed)
					return
				}
				assert.Equal(t, map[uuid.UUID]string{run.Metadata.Uuid: test.reported}, api.failed)
				assert.Equal(t, confirmations{run.Metadata.Uuid}, confirmed)
			})
		})
	}
}

func Test_Shutdown_LetsAClaimAndItsFailureReportFinish(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		run := newManualRun(t)
		api := &fakeApi{queue: []dispatcher.Run{run}, claimGate: make(chan struct{})}
		runs := newFakeDispatcher()
		runs.startErr = errors.New("no executor")
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error)
		go func() { done <- newPoller(testConfig(1), api, runs, func() {}, nil, nil).poll(ctx) }()
		synctest.Sleep(10 * time.Second)

		cancel()
		close(api.claimGate)
		synctest.Wait()
		close(runs.finished)

		require.NoError(t, <-done)
		assert.Equal(t, map[uuid.UUID]string{run.Metadata.Uuid: "Failed to start the run: no executor"}, api.failed)
	})
}
