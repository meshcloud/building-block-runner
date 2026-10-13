package dispatcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"testing"
	"testing/synctest"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/config/xtime"
	"github.com/meshcloud/building-block-runner/internal/http/meshapi"
)

var runnerUuid = uuid.MustParse("98520496-627d-43e6-82da-ce499179ff3f")

func newManualRun(t *testing.T) Run {
	t.Helper()
	run := Run{Metadata: meshapi.MeshRunnerRunMetadata{Uuid: uuid.New()}}
	run.Spec.RunToken = "token-" + run.Metadata.Uuid.String()
	run.Spec.Definition.Spec.Implementation = []byte(`{"type":"MANUAL"}`)
	return run
}

type fakeApi struct {
	mu        sync.Mutex
	queue     []Run
	failed    map[string]string
	claimSeen int
	patchErr  error
}

func (a *fakeApi) ClaimRun(_ context.Context, forRunner uuid.UUID) (meshapi.MeshRunnerRun, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if forRunner != runnerUuid {
		return Run{}, errors.New("claimed for another runner")
	}
	a.claimSeen++
	if len(a.queue) == 0 {
		return Run{}, meshapi.ErrNoRunToClaim
	}
	run := a.queue[0]
	a.queue = a.queue[1:]
	return run, nil
}

func (a *fakeApi) ForRun(run meshapi.MeshRunnerRun) meshapi.MeshRunnerRunClient {
	return fakeRunApi{a, run}
}

func (a *fakeApi) UpdateRegistration(context.Context, meshapi.MeshRunnerRegistration) error {
	return nil
}

func (a *fakeApi) failures() map[string]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.failed
}

type fakeRunApi struct {
	api *fakeApi
	run Run
}

func (r fakeRunApi) RegisterSource(_ context.Context, registration meshapi.MeshRunnerRunSourceRegistration) error {
	if registration.Source.Id != runnerUuid {
		return errors.New("registered another source")
	}
	return nil
}

// PatchStatus keys the summary by run token, as every request about a run must use its token.
func (r fakeRunApi) PatchStatus(_ context.Context, sourceId uuid.UUID, update meshapi.MeshRunnerRunStatusUpdate) (meshapi.MeshRunnerRunStatusUpdateResult, error) {
	if sourceId != runnerUuid || *update.Status != statusFailed {
		return meshapi.MeshRunnerRunStatusUpdateResult{}, errors.New("unexpected status update")
	}
	r.api.mu.Lock()
	defer r.api.mu.Unlock()
	if r.api.patchErr != nil {
		return meshapi.MeshRunnerRunStatusUpdateResult{}, r.api.patchErr
	}
	if r.api.failed == nil {
		r.api.failed = map[string]string{}
	}
	r.api.failed[r.run.Spec.RunToken] = *update.Summary
	return meshapi.MeshRunnerRunStatusUpdateResult{}, nil
}

func (fakeRunApi) DownloadPlanArtifact(context.Context, io.Writer) error { return nil }
func (fakeRunApi) UploadArtifact(context.Context, string) error          { return nil }

type fakeDispatcher struct {
	mu       sync.Mutex
	started  []Run
	startErr error
	status   chan RunStatus
}

func newFakeDispatcher() *fakeDispatcher {
	return &fakeDispatcher{status: make(chan RunStatus)}
}

func (d *fakeDispatcher) Start(_ context.Context, run Run) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.startErr != nil {
		return d.startErr
	}
	d.started = append(d.started, run)
	return nil
}

func (d *fakeDispatcher) Status() <-chan RunStatus {
	return d.status
}

func (d *fakeDispatcher) startedUuids() []uuid.UUID {
	d.mu.Lock()
	defer d.mu.Unlock()
	var uuids []uuid.UUID
	for _, run := range d.started {
		uuids = append(uuids, run.Metadata.Uuid)
	}
	return uuids
}

type fakeReadingDispatcher struct {
	*fakeDispatcher
	runs map[uuid.UUID]Run
}

func (d fakeReadingDispatcher) ReadRun(_ context.Context, runUuid uuid.UUID) (Run, error) {
	return d.runs[runUuid], nil
}

type fakeConfirmingDispatcher struct {
	*fakeDispatcher
	confirmed []uuid.UUID
}

func (d *fakeConfirmingDispatcher) FailureReported(_ context.Context, runUuid uuid.UUID) {
	d.confirmed = append(d.confirmed, runUuid)
}

func testConfig(maxConcurrentJobs int) config.Config {
	var cfg config.Config
	cfg.Uuid = runnerUuid
	cfg.Dispatcher.MaxConcurrentJobs = maxConcurrentJobs
	cfg.Dispatcher.PollingInterval = xtime.Seconds(10)
	return cfg
}

// startPolling runs in a synctest bubble. stop sends the statuses after the context ended and then closes the channel, as a dispatcher does.
func startPolling(t *testing.T, cfg config.Config, api *fakeApi, dispatcher Dispatcher, status chan RunStatus) (stop func(afterShutdown ...RunStatus) error) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	p := newPoller(cfg, log.New(io.Discard, "", 0), api, dispatcher)
	done := make(chan error)
	go func() { done <- p.poll(ctx) }()
	return func(afterShutdown ...RunStatus) error {
		cancel()
		synctest.Wait()
		for _, s := range afterShutdown {
			status <- s
		}
		close(status)
		return <-done
	}
}

func Test_Poll_ClaimsUpToCapacityAndAgainWhenARunEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		first, second, third := newManualRun(t), newManualRun(t), newManualRun(t)
		api := &fakeApi{queue: []Run{first, second, third}}
		dispatcher := newFakeDispatcher()
		stop := startPolling(t, testConfig(2), api, dispatcher, dispatcher.status)

		time.Sleep(10 * time.Second)
		synctest.Wait()
		assert.Equal(t, []uuid.UUID{first.Metadata.Uuid, second.Metadata.Uuid}, dispatcher.startedUuids())

		dispatcher.status <- RunStatus{RunUuid: first.Metadata.Uuid, Ended: true}
		synctest.Wait()
		assert.Equal(t, []uuid.UUID{first.Metadata.Uuid, second.Metadata.Uuid, third.Metadata.Uuid}, dispatcher.startedUuids())

		require.NoError(t, stop())
	})
}

func Test_Poll_RunsOfAPreviousProcessCountAgainstCapacity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := &fakeApi{queue: []Run{newManualRun(t)}}
		dispatcher := newFakeDispatcher()
		stop := startPolling(t, testConfig(1), api, dispatcher, dispatcher.status)

		dispatcher.status <- RunStatus{RunUuid: uuid.New()}
		time.Sleep(10 * time.Second)
		synctest.Wait()
		assert.Empty(t, dispatcher.startedUuids())
		assert.Zero(t, api.claimSeen)

		require.NoError(t, stop())
	})
}

func Test_Poll_ReportsAFailedStartWithTheRunToken(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		run := newManualRun(t)
		api := &fakeApi{queue: []Run{run}}
		dispatcher := newFakeDispatcher()
		dispatcher.startErr = errors.New("no executor")
		stop := startPolling(t, testConfig(1), api, dispatcher, dispatcher.status)

		time.Sleep(10 * time.Second)
		synctest.Wait()
		assert.Equal(t, map[string]string{run.Spec.RunToken: "Failed to start the run: no executor"}, api.failures())

		require.NoError(t, stop())
	})
}

func Test_Poll_ReportsARunThatEndedWithAnError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		run := newManualRun(t)
		api := &fakeApi{queue: []Run{run}}
		dispatcher := newFakeDispatcher()
		stop := startPolling(t, testConfig(1), api, dispatcher, dispatcher.status)
		time.Sleep(10 * time.Second)
		synctest.Wait()

		dispatcher.status <- RunStatus{RunUuid: run.Metadata.Uuid, Ended: true, Err: errors.New("OOMKilled")}
		synctest.Wait()
		assert.Equal(t, map[string]string{run.Spec.RunToken: "OOMKilled"}, api.failures())

		require.NoError(t, stop())
	})
}

func Test_Poll_SkipsTheReportOfAFailureTheRunReported(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		run := newManualRun(t)
		api := &fakeApi{queue: []Run{run}}
		dispatcher := newFakeDispatcher()
		stop := startPolling(t, testConfig(1), api, dispatcher, dispatcher.status)
		time.Sleep(10 * time.Second)
		synctest.Wait()

		dispatcher.status <- RunStatus{RunUuid: run.Metadata.Uuid, Ended: true, Err: fmt.Errorf("no logs dir: %w", ErrFailureReported)}
		synctest.Wait()
		assert.Empty(t, api.failures())

		require.NoError(t, stop())
	})
}

func Test_Poll_ReadsAFailedRunThatAPreviousProcessStarted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		run := newManualRun(t)
		api := &fakeApi{}
		dispatcher := fakeReadingDispatcher{newFakeDispatcher(), map[uuid.UUID]Run{run.Metadata.Uuid: run}}
		stop := startPolling(t, testConfig(1), api, dispatcher, dispatcher.status)

		dispatcher.status <- RunStatus{RunUuid: run.Metadata.Uuid, Ended: true, Err: errors.New("DeadlineExceeded")}
		synctest.Wait()
		assert.Equal(t, map[string]string{run.Spec.RunToken: "DeadlineExceeded"}, api.failures())

		require.NoError(t, stop())
	})
}

func Test_Poll_ReportsRunsThatEndAfterShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		run := newManualRun(t)
		api := &fakeApi{queue: []Run{run}}
		dispatcher := newFakeDispatcher()
		stop := startPolling(t, testConfig(1), api, dispatcher, dispatcher.status)
		time.Sleep(10 * time.Second)
		synctest.Wait()

		require.NoError(t, stop(RunStatus{RunUuid: run.Metadata.Uuid, Ended: true, Err: errors.New("panic")}))
		assert.Equal(t, map[string]string{run.Spec.RunToken: "panic"}, api.failures())
	})
}

func Test_Poll_ConfirmsOnlyAFailureReportThatMeshStackAccepted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rejected, accepted := newManualRun(t), newManualRun(t)
		api := &fakeApi{queue: []Run{rejected, accepted}, patchErr: errors.New("http error 503")}
		dispatcher := &fakeConfirmingDispatcher{fakeDispatcher: newFakeDispatcher()}
		stop := startPolling(t, testConfig(2), api, dispatcher, dispatcher.status)
		time.Sleep(10 * time.Second)
		synctest.Wait()

		dispatcher.status <- RunStatus{RunUuid: rejected.Metadata.Uuid, Ended: true, Err: errors.New("OOMKilled")}
		synctest.Wait()
		api.mu.Lock()
		api.patchErr = nil
		api.mu.Unlock()
		dispatcher.status <- RunStatus{RunUuid: accepted.Metadata.Uuid, Ended: true, Err: errors.New("OOMKilled")}
		synctest.Wait()

		require.NoError(t, stop())
		assert.Equal(t, []uuid.UUID{accepted.Metadata.Uuid}, dispatcher.confirmed)
	})
}
