package tf

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/meshcloud/building-block-runner/internal/dispatcher"
	"github.com/meshcloud/building-block-runner/internal/executor"
)

const (
	StepSources      = "sources"
	StepInput        = "input"
	StepInitTf       = "init_tf"
	StepPreRunScript = "pre_run_script"
	StepExecuteTf    = "execute_tf"
	StepOutput       = "output"
	StepTrigger      = "trigger"
)

type tfSteps struct {
	sources, input, initTf, preRunScript, execute, output *executor.Step
}

func declareSteps(r *executor.Reporter, behavior Behavior, async bool) tfSteps {
	if async {
		trigger := r.Step(StepTrigger, "Prepare Run")
		return tfSteps{trigger, trigger, trigger, trigger, trigger, trigger}
	}
	steps := tfSteps{
		sources:      r.Step(StepSources, "Prepare Run and download Sources"),
		input:        r.Step(StepInput, "Initialize Inputs"),
		initTf:       r.Step(StepInitTf, "Initialize Terraform and select Workspace"),
		preRunScript: r.Step(StepPreRunScript, "Execute Pre-Run Script"),
	}
	switch behavior {
	case behaviorDetect:
		steps.execute = r.Step(StepExecuteTf, "Run Terraform Plan")
	case behaviorDestroy:
		steps.execute = r.Step(StepExecuteTf, "Run Terraform Destroy")
	default:
		steps.execute = r.Step(StepExecuteTf, "Run Terraform Apply")
	}
	if behavior != behaviorDetect {
		steps.output = r.Step(StepOutput, "Collect Outputs and clean up")
	}
	return steps
}

type phase struct {
	step *executor.Step
	run  func() error
}

func (tfcmd *GenericCmd) runPhases(phases ...phase) error {
	for _, p := range phases {
		if err := p.step.Run(func() error { return tfcmd.runPhase(p) }); err != nil {
			return err
		}
	}
	if tfcmd.run.terraform().Async {
		tfcmd.reporter.HandOver()
	}
	return nil
}

func (tfcmd *GenericCmd) runPhase(p phase) error {
	err := p.run()
	if err == nil {
		return nil
	}
	slog.InfoContext(tfcmd.ctx, "the tf run failed", "error", err)
	message := err.Error()
	if strings.HasPrefix(message, "exit status ") {
		message = fmt.Sprintf("command failed (%s) — check the step logs above for error details.", message)
	}
	p.step.Logf("%s", message)
	switch {
	case errors.Is(tfcmd.ctx.Err(), context.DeadlineExceeded):
		message = fmt.Sprintf("execution step stopped: %v", context.Cause(tfcmd.ctx))
		p.step.Logf("%s", message)
	case errors.Is(tfcmd.ctx.Err(), context.Canceled):
		message = fmt.Sprintf("execution step cancelled: %v", context.Cause(tfcmd.ctx))
		p.step.Logf("%s", message)
	}
	return errors.New(message)
}

func (tfcmd *GenericCmd) preparePhases(tf *Facade, tfEnv *map[string]string) ([]phase, context.CancelFunc) {
	ctx, cancel := tfcmd.withInitTimeout(tfcmd.ctx, "the preparation (clone, tf install, init, workspace)")
	return []phase{
		{tfcmd.steps.sources, func() (err error) {
			*tf, *tfEnv, err = tfcmd.prepareSources(ctx)
			return err
		}},
		{tfcmd.steps.input, tfcmd.prepareInputs},
		{tfcmd.steps.initTf, func() error { return tfcmd.initWorkspace(ctx, *tf) }},
		{tfcmd.steps.preRunScript, func() error { return tfcmd.preRunScriptPhase(*tfEnv) }},
	}, cancel
}

func (tfcmd *GenericCmd) prepareSources(ctx context.Context) (Facade, map[string]string, error) {
	if err := tfcmd.source.Fetch(ctx, tfcmd.cfg); err != nil {
		return nil, nil, timedOut(ctx, "the git clone", err)
	}
	tf, err := tfcmd.bin.GetTF(ctx, tfcmd.moduleDir, tfcmd.tfVersion())
	if err != nil {
		return nil, nil, timedOut(ctx, "the tf install", err)
	}
	tfEnv, err := tfcmd.buildTfEnv()
	if err != nil {
		return nil, nil, err
	}
	if err := tf.SetEnv(tfEnv); err != nil {
		return nil, nil, err
	}
	return tf, tfEnv, nil
}

func (tfcmd *GenericCmd) prepareInputs() error {
	if _, err := tfcmd.saveInputFiles(); err != nil {
		return err
	}
	if err := tfcmd.linkKeycloakWifToken(); err != nil {
		return err
	}
	return tfcmd.vars()
}

func (tfcmd *GenericCmd) linkKeycloakWifToken() error {
	tokenFile := tfcmd.keycloakWifTokenFile
	if tokenFile == "" {
		return nil
	}
	link := filepath.Join(tfcmd.moduleDir, dispatcher.KeycloakWifTokenModulePath)
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		return err
	}
	return os.Symlink(tokenFile, link)
}

func (tfcmd *GenericCmd) initWorkspace(ctx context.Context, tf Facade) error {
	tfcmd.logTofuTo(tf, tfcmd.steps.initTf)
	if err := tfcmd.init(ctx, tf); err != nil {
		tfcmd.steps.initTf.Logf("Init with tf %s failed. %s", tfcmd.tfVersion(), initFailedHint)
		return err
	}
	return tfcmd.useWorkspaceIfNeeded(ctx, tf)
}

func (tfcmd *GenericCmd) preRunScriptPhase(tfEnv map[string]string) error {
	userMessage, err := tfcmd.runPreRunScript(tfEnv)
	if userMessage != "" {
		tfcmd.steps.preRunScript.UserLogf("%s", userMessage)
	}
	return err
}

func (*GenericCmd) logTofuTo(tf Facade, step *executor.Step) {
	tf.SetStdout(step)
	tf.SetStderr(step)
}
