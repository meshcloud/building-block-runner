package tfrun

import (
	"context"
	"os"
)

type TfDestroyCommand struct {
	GenericTfCmd
}

func DestroyCmd(ctx context.Context, params *TfCmdParams, tfbin *TfBinaries) *TfDestroyCommand {
	runContextInfo := ctx.Value(runInfoContextKey).(*RunContextInfo)

	return &TfDestroyCommand{
		GenericTfCmd{
			ctx:            ctx,
			runContextInfo: runContextInfo,
			bin:            tfbin,
			params:         params,
		},
	}
}

func (tfcmd *TfDestroyCommand) initRunSteps() {
	if tfcmd.runContextInfo.asyncRun {
		tfcmd.runContextInfo.runStatus.Steps = []*StepStatus{
			{
				Name:          StepTrigger,
				DisplayName:   "Prepare Run",
				Status:        PENDING,
				Outputs:       make(map[string]*TfOutput),
				UserMessage:   nil,
				SystemMessage: nil,
				LogStartIdx:   0,
			},
		}
	} else {
		tfcmd.runContextInfo.runStatus.Steps = []*StepStatus{
			{
				Name:          StepSources,
				DisplayName:   "Prepare Run and download Sources",
				Status:        PENDING,
				Outputs:       make(map[string]*TfOutput),
				UserMessage:   nil,
				SystemMessage: nil,
				LogStartIdx:   0,
			},
			{
				Name:          StepInput,
				DisplayName:   "Initialize Inputs",
				Status:        PENDING,
				Outputs:       make(map[string]*TfOutput),
				UserMessage:   nil,
				SystemMessage: nil,
				LogStartIdx:   0,
			},
			{
				Name:          StepInitTf,
				DisplayName:   "Initialize Terraform and select Workspace",
				Status:        PENDING,
				Outputs:       make(map[string]*TfOutput),
				UserMessage:   nil,
				SystemMessage: nil,
				LogStartIdx:   0,
			},
			{
				Name:          StepPreRunScript,
				DisplayName:   "Execute Pre-Run Script",
				Status:        PENDING,
				Outputs:       make(map[string]*TfOutput),
				UserMessage:   nil,
				SystemMessage: nil,
				LogStartIdx:   0,
			},
			{
				Name:          StepExecuteTf,
				DisplayName:   "Run Terraform",
				Status:        PENDING,
				Outputs:       make(map[string]*TfOutput),
				UserMessage:   nil,
				SystemMessage: nil,
				LogStartIdx:   0,
			},
			{
				Name:          StepOutput,
				DisplayName:   "Collect Outputs and clean up",
				Status:        PENDING,
				Outputs:       make(map[string]*TfOutput),
				UserMessage:   nil,
				SystemMessage: nil,
				LogStartIdx:   0,
			},
		}
	}

	tfcmd.runContextInfo.runStatus.CurrentStepIndex = 0
}

func (tfcmd *TfDestroyCommand) execute() {

	tfcmd.startRun()

	if err := tfcmd.createFreshCommandWd(); err != nil {
		tfcmd.fail(err)
		return
	}

	tf, err := tfcmd.bin.GetTF(tfcmd.ctx, tfcmd.runContextInfo.workingDirectory, tfcmd.params.tfVersion)
	if err != nil {
		tfcmd.fail(err)
		return
	}

	if err = tfcmd.setEnvWith(os.Setenv); err != nil {
		tfcmd.fail(err)
		return
	}

	tfcmd.assignOutput(tf)

	tfcmd.advanceStep(nil)

	if _, err = tfcmd.saveInputFiles(); err != nil {
		tfcmd.fail(err)
		return
	}

	if err := tfcmd.vars(); err != nil {
		tfcmd.fail(err)
		return
	}

	tfcmd.advanceStep(nil)

	if err = tfcmd.init(tf); err != nil {
		tfcmd.runContextInfo.logwrap.PrintlnToLocalAndUpdateLogs(HINT_INIT_FAILED)
		tfcmd.fail(err)
		return
	}

	if err = tfcmd.useWorkspaceIfNeeded(tf); err != nil {
		tfcmd.fail(err)
		return
	}

	tfcmd.advanceStep(nil)

	preRunUserMsg, err := tfcmd.runPreRunScript()
	if err != nil {
		tfcmd.fail(err)
		return
	}

	tfcmd.advanceStep(preRunUserMsg)

	// This is a hack to get the run id into the tf destroy command, by first performing an apply. This
	// is only required for async runs where the run object must be properly propagated into the external pipeline.
	if tfcmd.runContextInfo.asyncRun {
		// Variables are now in meshstack.auto.tfvars file, no command-line args needed
		if err = tf.Apply(tfcmd.ctx); err != nil {
			tfcmd.fail(err)
			return
		}
	}

	// Variables are now in meshstack.auto.tfvars file, no command-line args needed
	if err = tf.Destroy(tfcmd.ctx); err != nil {
		tfcmd.fail(err)
		return
	}

	tfcmd.advanceStep(nil)

	output, err := tf.Output(tfcmd.ctx)
	if err != nil {
		tfcmd.fail(err)
		return
	}

	tfcmd.collectOutput(output)
	tfcmd.deleteWorkspaceIfNeeded(tf)

	tfcmd.completeRun(nil)
}
