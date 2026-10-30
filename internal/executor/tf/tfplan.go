package tf

import (
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/meshcloud/meshstack-cli/client"
)

type PlanCommand struct {
	GenericCmd

	artifacts client.MeshRunnerRunClient
}

func (tfcmd *PlanCommand) execute() error {
	var tf Facade
	var tfEnv map[string]string
	prepare, cancelPreparation := tfcmd.preparePhases(&tf, &tfEnv)
	defer cancelPreparation()
	return tfcmd.runPhases(append(prepare,
		phase{tfcmd.steps.execute, func() error { return tfcmd.plan(tf) }},
	)...)
}

func (tfcmd *PlanCommand) plan(tf Facade) error {
	tfcmd.logTofuTo(tf, tfcmd.steps.execute)
	planFile := tfcmd.planFile()
	changed, err := tf.Plan(tfcmd.ctx, tfexec.Out(planFile), stateLockTimeout())
	if err != nil {
		return err
	}
	tfcmd.reporter.SetChangesDetected(changed)

	if tfcmd.run.Links.ArtifactUpload.Href == "" {
		return errors.New("no artifact upload URL provided for DETECT run; cannot persist plan for a follow-up APPLY")
	}
	if err := tfcmd.artifacts.UploadArtifact(tfcmd.ctx, planFile); err != nil {
		return fmt.Errorf("failed to upload artifact: %w", err)
	}
	return nil
}
