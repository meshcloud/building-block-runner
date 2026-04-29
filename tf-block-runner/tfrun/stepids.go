package tfrun

// Step ID constants for building block run steps.
// These IDs are used as stable identifiers in status updates sent to the meshStack API.
const (
	StepTrigger      = "trigger"        // async-mode only: single "prepare run" step
	StepSources      = "sources"        // clone/download source repository
	StepInput        = "input"          // prepare input variables
	StepInitTf       = "init_tf"        // terraform init + workspace selection
	StepPreRunScript = "pre_run_script" // optional pre-run hook script
	StepExecuteTf    = "execute_tf"     // terraform apply / destroy / plan
	StepOutput       = "output"         // collect outputs and clean up
)
