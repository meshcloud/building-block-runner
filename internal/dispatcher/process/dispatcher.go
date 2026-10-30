package process

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"slices"
	"sync"

	"github.com/meshcloud/building-block-runner/v2/internal/config"
	"github.com/meshcloud/building-block-runner/v2/internal/dispatcher"
	"github.com/meshcloud/building-block-runner/v2/internal/executor"
)

type Dispatcher struct {
	cfg        config.Config
	executable string
	runnerDir  *os.Root
	finished   chan dispatcher.Finished

	mu       sync.Mutex
	stopping bool
	runs     sync.WaitGroup
}

func New(ctx context.Context, cfg config.Config) (dispatcher.Dispatcher, error) {
	if err := checkExecutors(cfg.Dispatcher.ForTypes); err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("find the runner binary: %w", err)
	}
	runnerDir, err := openRunnerDir(cfg.RunnerDir())
	if err != nil {
		return nil, err
	}
	d := &Dispatcher{cfg: cfg, executable: executable, runnerDir: runnerDir, finished: make(chan dispatcher.Finished)}
	context.AfterFunc(ctx, d.stop)
	return d, nil
}

func checkExecutors(forTypes config.RunnerTypes) error {
	if slices.Contains(forTypes, config.RunnerTypeAll) {
		return nil
	}
	for _, runnerType := range forTypes {
		if !executor.Registered(runnerType) {
			return fmt.Errorf("this binary cannot run %s runs, set dispatcher.forTypes to the runner types it was built for", runnerType)
		}
	}
	return nil
}

func (*Dispatcher) Init(context.Context) (dispatcher.InitState, error) {
	return dispatcher.InitState{KeycloakWifTokenPath: dispatcher.KeycloakWifTokenModulePath}, nil
}

func (d *Dispatcher) Finished() <-chan dispatcher.Finished {
	return d.finished
}

func (d *Dispatcher) Dispatch(ctx context.Context, run dispatcher.Run, wifToken string) error {
	runnerType, err := executor.RunnerTypeOf(run)
	if err != nil {
		return err
	}
	if !d.cfg.Dispatcher.ForTypes.Contains(runnerType) {
		return fmt.Errorf("this runner does not run %s runs", runnerType)
	}
	env, err := d.childEnv(run, runnerType)
	if err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopping {
		return errors.New("the runner is shutting down")
	}
	runCtx, cancelRun := d.withTimeout(ctx)
	c := &child{run: run, root: d.runnerDir, killGrace: d.cfg.Dispatcher.KillGrace.Duration}
	if err := c.start(runCtx, d.executable, env, wifToken); err != nil {
		c.removeDirs(ctx)
		cancelRun()
		return err
	}
	d.runs.Go(func() {
		defer cancelRun()
		d.finished <- d.await(runCtx, c)
	})
	return nil
}

func (d *Dispatcher) stop() {
	d.mu.Lock()
	d.stopping = true
	d.mu.Unlock()
	d.runs.Wait()
	_ = d.runnerDir.Close()
	close(d.finished)
}

func (*Dispatcher) await(ctx context.Context, c *child) (finished dispatcher.Finished) {
	finished = dispatcher.Finished{RunUuid: c.run.Metadata.Uuid, ExitCode: exitUnknown}
	defer func() {
		if p := recover(); p != nil {
			slog.ErrorContext(ctx, "awaiting the run panicked", "panic", p, "stack", string(debug.Stack()))
			finished.ExitCode = exitUnknown
			finished.Failure = fmt.Sprintf("The runner failed while it awaited the run: %v", p)
		}
	}()
	defer c.removeDirs(ctx)
	finished.ExitCode = c.wait(ctx)
	if cause := context.Cause(ctx); cause != nil {
		finished.Failure = cause.Error()
	}
	return finished
}

func (d *Dispatcher) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := d.cfg.Dispatcher.Timeout.Duration
	if timeout == 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeoutCause(ctx, timeout, fmt.Errorf("the run exceeded the timeout of %s", timeout))
}
