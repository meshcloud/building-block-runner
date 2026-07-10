package tfrun

import (
	"fmt"
	"log"
	"os"
	"sync"
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
	shutdownCalled bool
	tfbinaries     *TfBinaries
	logger         *log.Logger
}

type RunManager interface {
	Start(*sync.WaitGroup)
	Stop()
}

func NewManager(tfbin *TfBinaries) RunManager {
	return &DefaultRunManager{
		defaultTimeout: time.Minute * time.Duration(AppConfig.TfCommandTimeoutMins),
		workerIn:       make(chan workerToken, 1),
		managerIn:      make(chan workerToken, 1),
		shutdownCalled: false,
		tfbinaries:     tfbin,
		logger:         log.New(os.Stdout, "[RunManager] ", log.LstdFlags),
	}
}

func (rm *DefaultRunManager) Start(wg *sync.WaitGroup) {
	rm.logger.Println("Started")
	os.MkdirAll(AppConfig.TfParentWorkingDir, 0777)
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
			workerDir:            AppConfig.TfParentWorkingDir,
			timeout:              timeout,
			workerIn:             rm.workerIn,
			workerOut:            rm.managerIn,
			runApi:               NewRunApi(),
			tfBinaries:           rm.tfbinaries,
			log:                  log.New(os.Stdout, fmt.Sprintf("[WORKER-%03d] ", i+1), log.LstdFlags),
			statusUpdateInterval: time.Second * 10,
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

	rm.logger.Println("Stopped")
}

func (rm *DefaultRunManager) handoutWorkerToken(delay time.Duration) {
	if rm.shutdownCalled {
		rm.workerIn <- stop
	} else {
		time.Sleep(delay)
		if rm.shutdownCalled {
			rm.workerIn <- stop
		} else {
			rm.workerIn <- work
		}
	}
}

func (rm *DefaultRunManager) Stop() {
	rm.shutdownCalled = true
	rm.logger.Println("Shutdown initialized")
}
