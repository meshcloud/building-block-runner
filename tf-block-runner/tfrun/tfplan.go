package tfrun

import (
	"context"
	"fmt"
	"os"

	"github.com/hashicorp/terraform-exec/tfexec"
)

type TfPlanCommand struct {
	GenericTfCmd
	runApi RunApi
}

func PlanCmd(ctx context.Context, params *TfCmdParams, tfbin *TfBinaries, runApi RunApi) *TfPlanCommand {
	runContextInfo := ctx.Value(runInfoContextKey).(*RunContextInfo)

	return &TfPlanCommand{
		GenericTfCmd: GenericTfCmd{
			ctx:            ctx,
			runContextInfo: runContextInfo,
			bin:            tfbin,
			params:         params,
		},
		runApi: runApi,
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
	changed, err := tf.Plan(tfcmd.ctx, tfexec.Out(planFile))
	if err != nil {
		tfcmd.fail(err)
		return
	}
	tfcmd.runContextInfo.runStatus.ChangesDetected = &changed

	// The backend always hands out a planArtifactUpload link for a DETECT run. A missing URL means the
	// plan could not be persisted, so a follow-up APPLY would have nothing to replay. Failing here is
	// safer than silently reporting SUCCEEDED with no retrievable plan.
	uploadUrl := tfcmd.params.planArtifactUploadUrl
	if uploadUrl == "" {
		tfcmd.fail(fmt.Errorf("no plan artifact upload URL provided for DETECT run; cannot persist plan for a follow-up APPLY"))
		return
	}

	planData, err := os.ReadFile(planFile)
	if err != nil {
		tfcmd.fail(fmt.Errorf("failed to read plan artifact: %w", err))
		return
	}

	// Upload the plan BEFORE completeRun: the terminal SUCCEEDED status update schedules the follow-up
	// APPLY run, whose predecessor plan-artifact lookup requires the bytes to already be stored.
	if err = tfcmd.runApi.UploadPlanArtifact(uploadUrl, planData); err != nil {
		tfcmd.fail(fmt.Errorf("failed to upload plan artifact: %w", err))
		return
	}

	tfcmd.completeRun(nil)
}
