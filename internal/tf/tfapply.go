package tf

import (
	"context"
	"fmt"
	"os"

	"github.com/hashicorp/terraform-exec/tfexec"
)

type TfApplyCommand struct {
	GenericTfCmd
	// runApi is the authenticated run API client (same one used for status updates). APPLY is the
	// only command that needs it: it downloads the predecessor plan artifact when
	// params.planArtifactUrl is set.
	runApi RunApi
}

func ApplyCmd(ctx context.Context, runContextInfo *RunContextInfo, params *TfCmdParams, tfbin *TfBinaries, runApi RunApi) *TfApplyCommand {
	return &TfApplyCommand{
		GenericTfCmd: GenericTfCmd{
			ctx:            ctx,
			runContextInfo: runContextInfo,
			bin:            tfbin,
			params:         params,
		},
		runApi: runApi,
	}
}

func (tfcmd *TfApplyCommand) initRunSteps() {
	if tfcmd.runContextInfo.asyncRun {
		tfcmd.runContextInfo.runStatus.Steps = []StepStatus{
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
		tfcmd.runContextInfo.runStatus.Steps = []StepStatus{
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
				DisplayName:   "Run Terraform Apply",
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

func (tfcmd *TfApplyCommand) execute() {

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

	if applyingPredecessorPlan := tfcmd.params.planArtifactUrl != ""; applyingPredecessorPlan {
		// This APPLY is linked to a predecessor DETECT run, so we replay the exact plan previewed
		// during that dry-run instead of computing a fresh one — the change is applied precisely as
		// it was reviewed and approved.
		if err = tfcmd.applyPredecessorPlan(tf); err != nil {
			tfcmd.fail(err)
			return
		}
	} else {
		// No predecessor plan artifact was linked to this APPLY run, so we run a fresh
		// terraform apply. Surfaced to the user so the two apply paths are distinguishable.
		tfcmd.PrintlnToLogsAndStep("No plan artifact linked to this run; running a fresh terraform apply.")
		// Variables are now in meshstack.auto.tfvars file, no command-line args needed
		if err = tf.Apply(tfcmd.ctx); err != nil {
			tfcmd.fail(err)
			return
		}
	}

	tfcmd.advanceStep(nil)

	output, err := tf.Output(tfcmd.ctx)
	if err != nil {
		tfcmd.fail(err)
		return
	}
	tfcmd.collectOutput(output)
	tfcmd.runContextInfo.filename_state = defaultStateFilename

	tfcmd.completeRun(nil)
}

// applyPredecessorPlan downloads the predecessor DETECT run's saved terraform plan and applies it
// verbatim via `terraform apply <plan>`. It must be called only after createFreshCommandWd() (which
// copies the source files into the per-run working directory) and after init, so the downloaded plan
// file and the initialized .terraform directory coexist. The plan bytes are written to the same
// <wd>/plan.tfplan path that the DETECT path produces.
//
// Known limitation — provider version drift: APPLY runs in a fresh working directory and re-runs
// `terraform init` (with -upgrade, see GenericTfCmd.init), so it re-selects provider versions
// independently of the DETECT run that produced this plan. If a provider publishes a new release
// between the dry-run and the approval, terraform will reject the saved plan because its embedded
// provider versions no longer match the freshly installed ones. This is inherent to applying a
// saved plan across separate runs without persisting the predecessor's .terraform.lock.hcl. We do
// not attempt to re-plan transparently; instead the apply below fails with an actionable message
// telling the user to re-run the dry-run. Committing .terraform.lock.hcl in the building block does
// not by itself avoid this, since init still runs with -upgrade.
func (tfcmd *TfApplyCommand) applyPredecessorPlan(tf TfFacade) error {
	tfcmd.PrintlnToLogsAndStep("Using the provided plan artifact from the dry-run; applying it verbatim instead of re-planning.")

	planFile := tfcmd.runContextInfo.artifactFilePath
	f, err := os.OpenFile(planFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create predecessor plan artifact file %s: %w", planFile, err)
	}
	// Stream the download straight to disk so a large terraform plan is never fully buffered in RAM.
	if err := tfcmd.runApi.DownloadPredecessorArtifact(tfcmd.params.planArtifactUrl, f); err != nil {
		_ = f.Close()
		// A planArtifact link was handed out only when the predecessor plan is genuinely available,
		// so a download failure here means the previewed plan can no longer be retrieved. Fail the
		// run rather than silently falling back to a fresh apply.
		return fmt.Errorf("failed to download the previewed terraform plan for this approval: %w. "+
			"The dry-run that produced this plan must be re-run before the change can be applied", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("failed to write predecessor plan artifact to %s: %w", planFile, err)
	}
	tfcmd.Printfln("Wrote predecessor plan artifact to %s", planFile)

	if err := tf.Apply(tfcmd.ctx, tfexec.DirOrPlan(planFile)); err != nil {
		// terraform rejects a saved plan whose state/config drifted since the dry-run
		// (e.g. "Saved plan is stale" / provider or state changes). Do not silently re-plan.
		return fmt.Errorf("applying the previewed terraform plan failed: %w. "+
			"The previewed plan is no longer valid — the underlying state, configuration, or providers "+
			"changed since the dry-run. Please re-run the dry-run to produce and approve a fresh plan", err)
	}

	return nil
}
