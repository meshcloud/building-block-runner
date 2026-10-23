package tf

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/meshcloud/meshstack-cli/client"
)

type TfApplyCommand struct {
	GenericTfCmd

	artifacts client.MeshRunnerRunClient
}

func (tfcmd *TfApplyCommand) execute() error {
	var tf TfFacade
	var tfEnv map[string]string
	return tfcmd.runPhases(append(tfcmd.preparePhases(&tf, &tfEnv),
		phase{tfcmd.steps.execute, func() error { return tfcmd.apply(tf) }},
		phase{tfcmd.steps.output, func() error { return tfcmd.collectOutputs(tf) }},
	)...)
}

func (tfcmd *TfApplyCommand) apply(tf TfFacade) error {
	tfcmd.logTofuTo(tf, tfcmd.steps.execute)
	// A plan that a dry-run previewed and someone approved is applied as it is.
	if tfcmd.run.Links.Artifact.Href != "" {
		return tfcmd.applyPredecessorPlan(tf)
	}
	tfcmd.steps.execute.Logf("No plan artifact linked to this run; running a fresh terraform apply.")
	return tf.Apply(tfcmd.ctx, stateLockTimeout())
}

func (tfcmd *TfApplyCommand) collectOutputs(tf TfFacade) error {
	tfcmd.logTofuTo(tf, tfcmd.steps.output)
	output, err := tf.Output(tfcmd.ctx)
	if err != nil {
		return err
	}
	return tfcmd.collectOutput(output)
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
	tfcmd.steps.execute.Logf("Using the provided plan artifact from the dry-run; applying it verbatim instead of re-planning.")

	planFile := tfcmd.planFile()
	f, err := os.OpenFile(planFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("failed to create predecessor plan artifact file %s: %w", planFile, err)
	}
	// Stream the download straight to disk so a large terraform plan is never fully buffered in RAM.
	if err := tfcmd.artifacts.DownloadArtifact(tfcmd.ctx, f); err != nil {
		_ = f.Close()
		// A fresh apply here would bypass the plan the approver previewed.
		return fmt.Errorf("failed to download the previewed terraform plan for this approval: %w. "+
			"The dry-run that produced this plan must be re-run before the change can be applied", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("failed to write predecessor plan artifact to %s: %w", planFile, err)
	}
	slog.InfoContext(tfcmd.ctx, "wrote the plan artifact of the dry-run", "file", planFile)

	if err := tf.Apply(tfcmd.ctx, tfexec.DirOrPlan(planFile), stateLockTimeout()); err != nil {
		// terraform rejects a saved plan whose state/config drifted since the dry-run
		// (e.g. "Saved plan is stale" / provider or state changes). Do not silently re-plan.
		return fmt.Errorf("applying the previewed terraform plan failed: %w. "+
			"The previewed plan is no longer valid — the underlying state, configuration, or providers "+
			"changed since the dry-run. Please re-run the dry-run to produce and approve a fresh plan", err)
	}

	return nil
}
