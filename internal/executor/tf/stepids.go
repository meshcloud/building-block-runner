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
	StepTrigger      = "trigger"
	StepSources      = "sources"
	StepInput        = "input"
	StepInitTf       = "init_tf"
	StepPreRunScript = "pre_run_script"
	StepExecuteTf    = "execute_tf"
	StepOutput       = "output"
)

// tfSteps points every field at the single trigger step of an async run.
type tfSteps struct {
	async                                                 bool
	sources, input, initTf, preRunScript, execute, output *executor.Step
}

func declareSteps(r *executor.Reporter, async bool, behavior Behavior) tfSteps {
	if async {
		trigger := r.Step(StepTrigger, "Prepare Run")
		return tfSteps{async: true, sources: trigger, input: trigger, initTf: trigger, preRunScript: trigger, execute: trigger, output: trigger}
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

func (tfcmd *GenericTfCmd) runPhases(phases ...phase) error {
	if tfcmd.steps.async {
		return tfcmd.steps.sources.Run(func() error {
			for _, p := range phases {
				if err := tfcmd.runPhase(p); err != nil {
					return err
				}
			}
			return nil
		})
	}
	for _, p := range phases {
		if err := p.step.Run(func() error { return tfcmd.runPhase(p) }); err != nil {
			return err
		}
	}
	return nil
}

func (tfcmd *GenericTfCmd) runPhase(p phase) error {
	err := p.run()
	if err == nil {
		return nil
	}
	slog.InfoContext(tfcmd.ctx, "the tf run failed", "error", err)
	// "exit status 1" is not actionable, while the step log holds the errors of the command.
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

func (tfcmd *GenericTfCmd) preparePhases(tf *TfFacade, tfEnv *map[string]string) []phase {
	return []phase{
		{tfcmd.steps.sources, func() (err error) {
			*tf, *tfEnv, err = tfcmd.prepareSources()
			return err
		}},
		{tfcmd.steps.input, tfcmd.prepareInputs},
		{tfcmd.steps.initTf, func() error { return tfcmd.initWorkspace(*tf) }},
		{tfcmd.steps.preRunScript, func() error { return tfcmd.preRunScriptPhase(*tfEnv) }},
	}
}

func (tfcmd *GenericTfCmd) prepareSources() (TfFacade, map[string]string, error) {
	if err := tfcmd.source.Fetch(tfcmd.ctx, tfcmd.cfg); err != nil {
		return nil, nil, err
	}
	tf, err := tfcmd.bin.GetTF(tfcmd.ctx, tfcmd.moduleDir, tfcmd.tfVersion())
	if err != nil {
		return nil, nil, err
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

func (tfcmd *GenericTfCmd) prepareInputs() error {
	if _, err := tfcmd.saveInputFiles(); err != nil {
		return err
	}
	if err := tfcmd.linkKeycloakWifToken(); err != nil {
		return err
	}
	return tfcmd.vars()
}

func (tfcmd *GenericTfCmd) linkKeycloakWifToken() error {
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

func (tfcmd *GenericTfCmd) initWorkspace(tf TfFacade) error {
	tfcmd.logTofuTo(tf, tfcmd.steps.initTf)
	if err := tfcmd.init(tf); err != nil {
		tfcmd.steps.initTf.Logf("Init with tf %s failed. %s", tfcmd.tfVersion(), initFailedHint)
		return err
	}
	return tfcmd.useWorkspaceIfNeeded(tf)
}

func (tfcmd *GenericTfCmd) preRunScriptPhase(tfEnv map[string]string) error {
	userMessage, err := tfcmd.runPreRunScript(tfEnv)
	if userMessage != "" {
		tfcmd.steps.preRunScript.UserLogf("%s", userMessage)
	}
	return err
}

func (*GenericTfCmd) logTofuTo(tf TfFacade, step *executor.Step) {
	tf.SetStdout(step)
	tf.SetStderr(step)
}
