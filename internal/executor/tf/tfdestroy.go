package tf

import (
	"github.com/hashicorp/go-version"
	"github.com/hashicorp/terraform-exec/tfexec"
)

// OpenTofu added -suppress-forget-errors in 1.12.0; versions up to 1.5.5 run Terraform (see GetTF), which lacks it.
var suppressForgetErrorsMinVersion = version.Must(version.NewVersion("1.12.0"))

type TfDestroyCommand struct {
	GenericTfCmd
}

func (tfcmd *TfDestroyCommand) execute() error {
	var tf TfFacade
	var tfEnv map[string]string
	return tfcmd.runPhases(append(tfcmd.preparePhases(&tf, &tfEnv),
		phase{tfcmd.steps.execute, func() error { return tfcmd.destroy(tf) }},
		phase{tfcmd.steps.output, func() error { return tfcmd.collectOutputsAndDeleteWorkspace(tf) }},
	)...)
}

func (tfcmd *TfDestroyCommand) destroy(tf TfFacade) error {
	tfcmd.logTofuTo(tf, tfcmd.steps.execute)
	if tfcmd.run.terraform().Async {
		if err := tf.Apply(tfcmd.ctx, stateLockTimeout()); err != nil {
			return err
		}
	}
	return tf.Destroy(tfcmd.ctx, destroyOptions(tfcmd.tfVersion())...)
}

func (tfcmd *TfDestroyCommand) collectOutputsAndDeleteWorkspace(tf TfFacade) error {
	tfcmd.logTofuTo(tf, tfcmd.steps.output)
	output, err := tf.Output(tfcmd.ctx)
	if err != nil {
		return err
	}
	if err := tfcmd.collectOutput(output); err != nil {
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
