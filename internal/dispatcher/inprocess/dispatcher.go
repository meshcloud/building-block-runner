package inprocess

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/meshcloud/meshstack-cli/client"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatcher"
)

type Dispatcher struct {
	logger    *log.Logger
	api       client.MeshRunnerClient
	executors executors
	timeout   time.Duration
	// runCtx outlives the context given to New, so that a shutdown waits for the runs.
	runCtx context.Context
	status chan dispatcher.RunStatus

	mu       sync.Mutex
	stopping bool
	runs     sync.WaitGroup
}

func New(ctx context.Context, cfg config.Config, api client.MeshRunnerClient) (dispatcher.Dispatcher, error) {
	executors, err := newExecutors(cfg)
	if err != nil {
		return nil, err
	}
	return newDispatcher(ctx, api, executors, cfg.Dispatcher.Timeout.Duration), nil
}

func newDispatcher(ctx context.Context, api client.MeshRunnerClient, executors executors, timeout time.Duration) *Dispatcher {
	d := &Dispatcher{
		logger:    log.New(os.Stdout, "[IN-PROCESS] ", log.LstdFlags),
		api:       api,
		executors: executors,
		timeout:   timeout,
		runCtx:    context.WithoutCancel(ctx),
		status:    make(chan dispatcher.RunStatus),
	}
	context.AfterFunc(ctx, d.stop)
	return d
}

func (d *Dispatcher) stop() {
	d.mu.Lock()
	d.stopping = true
	d.mu.Unlock()
	d.runs.Wait()
	close(d.status)
}

func (d *Dispatcher) Status() <-chan dispatcher.RunStatus {
	return d.status
}

func (d *Dispatcher) Start(_ context.Context, run dispatcher.Run) error {
	runnerType, err := dispatcher.RunnerTypeOf(run)
	if err != nil {
		return err
	}
	execute, ok := d.executors[runnerType]
	if !ok {
		return fmt.Errorf("this runner cannot run %s runs", runnerType)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopping {
		return errors.New("the runner is shutting down")
	}
	d.runs.Go(func() {
		err := d.execute(execute, run)
		d.status <- dispatcher.RunStatus{RunUuid: run.Metadata.Uuid, Ended: true, Err: err}
	})
	return nil
}

func (d *Dispatcher) execute(execute executeFunc, run dispatcher.Run) (err error) {
	ctx, cancel := d.withTimeout()
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			d.logger.Printf("Run panicked: %v\n%s", r, debug.Stack())
			err = fmt.Errorf("the run panicked: %v", r)
		}
	}()
	err = execute(ctx, run, d.api.ForRun(run))
	if err == nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = context.Cause(ctx)
	}
	return
}

func (d *Dispatcher) withTimeout() (context.Context, context.CancelFunc) {
	if d.timeout == 0 {
		return context.WithCancel(d.runCtx)
	}
	return context.WithTimeoutCause(d.runCtx, d.timeout, fmt.Errorf("the run exceeded the timeout of %s", d.timeout))
}
