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
	executors executorsByType
	timeout   time.Duration
	finished  chan dispatcher.RunFinished

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

func newDispatcher(ctx context.Context, api client.MeshRunnerClient, executors executorsByType, timeout time.Duration) *Dispatcher {
	d := &Dispatcher{
		logger:    log.New(os.Stdout, "[IN-PROCESS] ", log.LstdFlags),
		api:       api,
		executors: executors,
		timeout:   timeout,
		finished:  make(chan dispatcher.RunFinished),
	}
	context.AfterFunc(ctx, d.stop)
	return d
}

func (*Dispatcher) Init(context.Context) (dispatcher.InitState, error) {
	return dispatcher.InitState{}, nil
}

func (d *Dispatcher) Finished() <-chan dispatcher.RunFinished {
	return d.finished
}

func (d *Dispatcher) Start(ctx context.Context, run dispatcher.Run) error {
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
	// A run outlives a shutdown, which waits for it.
	runCtx := context.WithoutCancel(ctx)
	d.runs.Go(func() {
		err := d.execute(runCtx, execute, run)
		d.finished <- dispatcher.RunFinished{RunUuid: run.Metadata.Uuid, Err: err}
	})
	return nil
}

func (d *Dispatcher) stop() {
	d.mu.Lock()
	d.stopping = true
	d.mu.Unlock()
	d.runs.Wait()
	close(d.finished)
}

func (d *Dispatcher) execute(ctx context.Context, execute executeFunc, run dispatcher.Run) (err error) {
	ctx, cancel := d.withTimeout(ctx)
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

func (d *Dispatcher) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if d.timeout == 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeoutCause(ctx, d.timeout, fmt.Errorf("the run exceeded the timeout of %s", d.timeout))
}
