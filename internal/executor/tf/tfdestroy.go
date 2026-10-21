package tf

import (
	"github.com/hashicorp/go-version"
	"github.com/hashicorp/terraform-exec/tfexec"
)

// Every version above 1.5.5 runs OpenTofu, which added the flag in 1.12.0.
var suppressForgetErrorsMinVersion = version.Must(version.NewVersion("1.12.0"))

type DestroyCommand struct {
	GenericCmd
}

func (tfcmd *DestroyCommand) execute() error {
	var tf Facade
	var tfEnv map[string]string
	prepare, cancelPreparation := tfcmd.preparePhases(&tf, &tfEnv)
	defer cancelPreparation()
	return tfcmd.runPhases(append(prepare,
		phase{tfcmd.steps.execute, func() error { return tfcmd.destroy(tf) }},
		phase{tfcmd.steps.output, func() error { return tfcmd.collectOutputsAndDeleteWorkspace(tf) }},
	)...)
}

func (tfcmd *DestroyCommand) destroy(tf Facade) error {
	tfcmd.logTofuTo(tf, tfcmd.steps.execute)
	// A destroy sees the inputs of the last apply, so the apply hands this run's token to the pipeline the destroy starts.
	if tfcmd.run.terraform().Async {
		if err := tf.Apply(tfcmd.ctx, stateLockTimeout()); err != nil {
			return err
		}
	}
	return tf.Destroy(tfcmd.ctx, destroyOptions(tfcmd.tfVersion())...)
}

func (tfcmd *DestroyCommand) collectOutputsAndDeleteWorkspace(tf Facade) error {
	if err := tfcmd.collectOutputs(tf); err != nil {
		return err
	}
	tfcmd.deleteWorkspaceIfNeeded(tf)
	return nil
}

// destroyOptions lets OpenTofu finish a destroy that forgets resources with lifecycle { destroy = false }.
func destroyOptions(tfVersion string) []tfexec.DestroyOption {
	options := []tfexec.DestroyOption{stateLockTimeout()}
	v, err := version.NewVersion(tfVersion)
	if err != nil || v.LessThan(suppressForgetErrorsMinVersion) {
		return options
	}
	// terraform-exec has no option for this flag; Dir appends it as the last argument.
	return append(options, tfexec.Dir("-suppress-forget-errors"))
}
