package process

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"

	"github.com/meshcloud/building-block-runner/internal/dispatcher"
	"github.com/meshcloud/building-block-runner/internal/executor"
)

const exitUnknown = -1

type child struct {
	run       dispatcher.Run
	root      *os.Root
	dirs      []string
	cmd       *exec.Cmd
	killGrace time.Duration
}

func (c *child) start(ctx context.Context, executable string, env []string) error {
	runDir := filepath.Join("runs", c.run.Metadata.Uuid.String())
	if err := c.freshDir(runDir); err != nil {
		return err
	}
	runYml := filepath.Join(runDir, "run.yml")
	if err := c.writeRunYml(runYml); err != nil {
		return err
	}
	workDir := filepath.Join(runDir, "work")
	if err := c.root.Mkdir(workDir, 0o700); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, executable)
	cmd.Dir = c.path(workDir)
	cmd.Env = slices.Concat(env, []string{executor.EnvRunYml + "=" + c.path(runYml)})
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	startInOwnGroup(cmd)
	lifeline, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	cmd.Cancel = func() error {
		_, _ = io.WriteString(lifeline, context.Cause(ctx).Error()+"\n")
		return lifeline.Close()
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start the run: %w", err)
	}
	c.cmd = cmd
	return nil
}

func (c *child) freshDir(name string) error {
	if err := c.root.RemoveAll(name); err != nil {
		return err
	}
	if err := c.root.MkdirAll(name, 0o700); err != nil {
		return err
	}
	c.dirs = append(c.dirs, name)
	return nil
}

func (c *child) path(name string) string {
	return filepath.Join(c.root.Name(), name)
}

func (c *child) writeRunYml(name string) error {
	encoded, err := json.Marshal(c.run)
	if err != nil {
		return err
	}
	return c.root.WriteFile(name, encoded, 0o600)
}

func (c *child) wait(ctx context.Context) int {
	exited := make(chan struct{})
	defer close(exited)
	stopWatching := context.AfterFunc(ctx, func() { c.killAfterGrace(ctx, exited) })
	defer stopWatching()
	_ = c.cmd.Wait()
	if c.cmd.ProcessState == nil {
		return exitUnknown
	}
	return c.cmd.ProcessState.ExitCode()
}

func (c *child) killAfterGrace(ctx context.Context, exited <-chan struct{}) {
	select {
	case <-exited:
	case <-time.After(c.killGrace):
		slog.WarnContext(ctx, "the run did not exit after its stdin closed, killing it", "after", c.killGrace)
		if err := killGroup(c.cmd); err != nil {
			slog.ErrorContext(ctx, "killing the run failed", "error", err)
		}
	}
}

func (c *child) removeDirs(ctx context.Context) {
	for _, dir := range c.dirs {
		if err := c.root.RemoveAll(dir); err != nil {
			slog.WarnContext(ctx, "removing a dir of the run failed", "dir", c.path(dir), "error", err)
		}
	}
}
