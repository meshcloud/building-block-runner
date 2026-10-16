package tfrun

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-exec/tfexec"
)

type TfPlanCommand struct {
	GenericTfCmd
	runApi RunApi
}

func PlanCmd(ctx context.Context, params *TfCmdParams, tfbin *TfBinaries, runApi RunApi) *TfPlanCommand {
	runContextInfo := ctx.Value(runInfoContextKey).(*RunContextInfo)

	return &TfPlanCommand{
		ctx:            ctx,
		runContextInfo: runContextInfo,
		bin:            tfbin,
		params:         params,
		runApi:         runApi,
	}
}

func (tfcmd *TfPlanCommand) initRunSteps() {
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
				DisplayName:   "Run Terraform Plan",
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

func (tfcmd *TfPlanCommand) execute() {

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

	tfEnv, err := tfcmd.buildTfEnv()
	if err != nil {
		tfcmd.fail(err)
		return
	}
	if err = tf.SetEnv(tfEnv); err != nil {
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

	preRunUserMsg, err := tfcmd.runPreRunScript(tfEnv)
	if err != nil {
		tfcmd.failWithUserMsg(err, preRunUserMsg)
		return
	}

	tfcmd.advanceStep(preRunUserMsg)

	// Variables are now in meshstack.auto.tfvars file, no command-line args needed
	planFile := tfcmd.runContextInfo.artifactFilePath
	// Plan runs `terraform plan -detailed-exitcode`; changed is true when the plan found changes.
	changed, err := tf.Plan(tfcmd.ctx, tfexec.Out(planFile), stateLockTimeout())
	if err != nil {
		tfcmd.fail(err)
		return
	}
	tfcmd.runContextInfo.runStatus.ChangesDetected = &changed

	// A missing upload link is a meshStack bug, and a follow-up APPLY would have no plan to apply.
	if tfcmd.params.artifactUploadUrl == "" {
		tfcmd.fail(fmt.Errorf("no artifact upload URL provided for DETECT run; cannot persist plan for a follow-up APPLY"))
		return
	}

	// The SUCCEEDED status schedules the follow-up APPLY, which needs the plan already stored.
	if err = tfcmd.runApi.UploadArtifact(tfcmd.ctx, planFile); err != nil {
		tfcmd.fail(fmt.Errorf("failed to upload artifact: %w", err))
		return
	}

	tfcmd.completeRun(nil)
}
