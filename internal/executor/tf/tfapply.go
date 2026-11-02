package tf

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/meshcloud/meshstack-cli/client"
)

type ApplyCommand struct {
	GenericCmd

	artifacts client.MeshRunnerRunClient
}

func (tfcmd *ApplyCommand) execute() error {
	var tf Facade
	var tfEnv map[string]string
	prepare, cancelPreparation := tfcmd.preparePhases(&tf, &tfEnv)
	defer cancelPreparation()
	return tfcmd.runPhases(append(prepare,
		phase{tfcmd.steps.execute, func() error { return tfcmd.apply(tf) }},
		phase{tfcmd.steps.output, func() error { return tfcmd.collectOutputs(tf) }},
	)...)
}

func (tfcmd *ApplyCommand) apply(tf Facade) error {
	tfcmd.logTofuTo(tf, tfcmd.steps.execute)
	if tfcmd.run.approvedPlan() {
		return tfcmd.applyPredecessorPlan(tf)
	}
	tfcmd.steps.execute.Logf("No plan artifact linked to this run; running a fresh terraform apply.")
	return tf.Apply(tfcmd.ctx, stateLockTimeout())
}

func (tfcmd *ApplyCommand) applyPredecessorPlan(tf Facade) error {
	tfcmd.steps.execute.Logf("Using the provided plan artifact from the dry-run; applying it verbatim instead of re-planning.")

	planFile := tfcmd.planFile()
	f, err := os.OpenFile(planFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("failed to create predecessor plan artifact file %s: %w", planFile, err)
	}
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
		return fmt.Errorf("applying the previewed terraform plan failed: %w. "+
			"The previewed plan is no longer valid — the underlying state, configuration, or providers "+
			"changed since the dry-run. Please re-run the dry-run to produce and approve a fresh plan", err)
	}

	return nil
}
