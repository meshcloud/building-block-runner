package tf

import (
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/meshcloud/meshstack-cli/client"
)

type TfPlanCommand struct {
	GenericTfCmd

	artifacts client.MeshRunnerRunClient
}

func (tfcmd *TfPlanCommand) execute() error {
	var tf TfFacade
	var tfEnv map[string]string
	return tfcmd.runPhases(append(tfcmd.preparePhases(&tf, &tfEnv),
		phase{tfcmd.steps.execute, func() error { return tfcmd.plan(tf) }},
	)...)
}

func (tfcmd *TfPlanCommand) plan(tf TfFacade) error {
	tfcmd.logTofuTo(tf, tfcmd.steps.execute)
	planFile := tfcmd.planFile()
	changed, err := tf.Plan(tfcmd.ctx, tfexec.Out(planFile), stateLockTimeout())
	if err != nil {
		return err
	}
	tfcmd.reporter.SetChangesDetected(changed)

	// A missing upload link is a meshStack bug, and a follow-up APPLY would have no plan to apply.
	if tfcmd.run.Links.ArtifactUpload.Href == "" {
		return errors.New("no artifact upload URL provided for DETECT run; cannot persist plan for a follow-up APPLY")
	}
	// The SUCCEEDED status schedules the follow-up APPLY, which needs the plan already stored.
	if err := tfcmd.artifacts.UploadArtifact(tfcmd.ctx, planFile); err != nil {
		return fmt.Errorf("failed to upload artifact: %w", err)
	}
	return nil
}
