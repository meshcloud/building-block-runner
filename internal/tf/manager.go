package tf

import (
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type workerToken int

const FAILED_WORKER_DELAY = 60 * time.Second
const NORUN_WORKER_DELAY = 10 * time.Second

const (
	work = workerToken(iota)
	done
	norun
	failed
	stop
	stopped
)

type DefaultRunManager struct {
	workerIn       chan workerToken // commands towards worker routines
	managerIn      chan workerToken // channel is read by run manager
	defaultTimeout time.Duration
	// shutdownCalled is read by the handout goroutine and written by Stop concurrently, so it
	// must be atomic (B6 fix — the former plain bool was a data race).
	shutdownCalled atomic.Bool
	tfbinaries     *TfBinaries
	logger         *slog.Logger
	dec            Decryptor
	meter          Meter
	// cfg is the runner config threaded explicitly (FOLLOW_UP P2.3) in place of the former
	// AppConfig global; used to build the worker's run API and execution config.
	cfg TfRunnerConfig
}

type RunManager interface {
	Start(*sync.WaitGroup)
	Stop()
}

// NewManager wires the polling run manager. cfg is the runner config threaded explicitly
// (FOLLOW_UP P2.3) in place of the former AppConfig global. meter receives the D12 generic
// standalone-runner metrics (§4.3); pass NoopMeter{} where no *mgmt.RunMetrics is
// available (e.g. a caller that has not wired D12 yet). logger is the persona logger,
// injected (P3) so worker/run-scoped children carry the persona attribute (§8.1).
func NewManager(cfg TfRunnerConfig, tfbin *TfBinaries, dec Decryptor, meter Meter, logger *slog.Logger) RunManager {
	return &DefaultRunManager{
		defaultTimeout: time.Minute * time.Duration(cfg.TfCommandTimeoutMins),
		workerIn:       make(chan workerToken, 1),
		managerIn:      make(chan workerToken, 1),
		tfbinaries:     tfbin,
		logger:         logger.With("component", "runManager"),
		dec:            dec,
		meter:          meter,
		cfg:            cfg,
	}
}

func (rm *DefaultRunManager) Start(wg *sync.WaitGroup) {
	rm.logger.Info("Started")
	if err := os.MkdirAll(rm.cfg.TfParentWorkingDir, 0777); err != nil {
		rm.logger.Error("failed to create working directory", "dir", rm.cfg.TfParentWorkingDir, "error", err)
	}
	go func() {
		defer wg.Done()
		rm.run(rm.defaultTimeout)
	}()
}

func (rm *DefaultRunManager) run(timeout time.Duration) {

	// hand out worker token (single worker)
	rm.workerIn <- work

	// start the worker
	for i := 0; i < 1; i++ {
		worker := &Worker{
			workerNumber:         i + 1,
			workerDir:            rm.cfg.TfParentWorkingDir,
			timeout:              timeout,
			workerIn:             rm.workerIn,
			workerOut:            rm.managerIn,
			runApi:               NewRunApi(rm.cfg.RunApiBackend, rm.cfg.RunnerUuid, rm.dec),
			tfBinaries:           rm.tfbinaries,
			log:                  rm.logger.With("worker", i+1),
			statusUpdateInterval: time.Second * 10,
			dec:                  rm.dec,
			meter:                rm.meter,
			cfg:                  rm.cfg.exec(),
		}
		go worker.work()
	}

	rm.handleWorkers()
}

func (rm *DefaultRunManager) handleWorkers() {
	stoppedWorkers := 0
	handle := true

	for handle {
		fromWorkerToken := <-rm.managerIn

		switch fromWorkerToken {

		// if a worker is done, hand out a new token.
		// in case shutdown was called, send out a stop token instead.
		case done:
			go rm.handoutWorkerToken(0)

		// if a worker has no available run to execute, hand out a new token, but with a delay
		// in case shutdown was called, send out a stop token instead.
		case norun:
			go rm.handoutWorkerToken(NORUN_WORKER_DELAY)

		// if an error occurred while fetching a run, hand out a new token, but with a delay
		// in case shutdown was called, send out a stop token instead.
		case failed:
			go rm.handoutWorkerToken(FAILED_WORKER_DELAY)

		// if a worker was stopped, track it and shutdown, if this was the last one
		case stopped:
			stoppedWorkers++
			if stoppedWorkers == 1 {
				handle = false
			}
		}
	}

	rm.logger.Info("Stopped")
}

func (rm *DefaultRunManager) handoutWorkerToken(delay time.Duration) {
	if rm.shutdownCalled.Load() {
		rm.workerIn <- stop
	} else {
		time.Sleep(delay)
		if rm.shutdownCalled.Load() {
			rm.workerIn <- stop
		} else {
			rm.workerIn <- work
		}
	}
}

func (rm *DefaultRunManager) Stop() {
	rm.shutdownCalled.Store(true)
	rm.logger.Info("Shutdown initialized")
}
