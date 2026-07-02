package tfrun

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/hashicorp/terraform-exec/tfexec"
	meshcrypto "github.com/meshcloud/building-block-runner/go-meshapi-client/crypto"
	"github.com/meshcloud/building-block-runner/tf-block-runner/util"
	"github.com/zclconf/go-cty/cty"
)

const (
	defaultStateFilename = "terraform.tfstate"

	HINT_INIT_FAILED = "Check provider and / or backend config"

	// MeshStackRunTokenBasicUser is the Basic-auth username sent with the state backend
	// request. Its value is arbitrary and ignored by meshfed-api: the run JWT rides in
	// the Basic password, and meshfed-api's TfStateRunTokenBasicAuthFilter reads only the
	// password and interprets it as the token, rewriting Basic(<any-user>:<jwt>) to
	// Bearer <jwt> for the state endpoint. We send a placeholder "x" to make this explicit.
	MeshStackRunTokenBasicUser = "x"
)

type TfCmdParams struct {
	dir                string
	buildingBlockId    string
	tfVersion          string
	useWorkspaces      bool
	suggestedWorkspace string
	vars               map[string]*Variable
	source             *GitSource
	preRunScript       *string
	runMode            string
	// planArtifactUrl is copied from Run.PlanArtifactUrl; empty means plain apply.
	planArtifactUrl string
}

// GenericTfCmd implements generic functionality all TF commands re-use
type GenericTfCmd struct {
	ctx            context.Context
	runContextInfo *RunContextInfo // is actually part of the context, but extracted for easier access w/o cast
	bin            *TfBinaries
	params         *TfCmdParams
}

type TfStepName string

type TfCmd interface {
	initRunSteps()
	execute()
	setCurrentStepMessage(userMessage *string)
	nextStep()
	fail(err error)
}

func (tfcmd *GenericTfCmd) Println(v ...any) {
	tfcmd.runContextInfo.logwrap.PrintlnToLocalLogs(v...)
}

func (tfcmd *GenericTfCmd) Printfln(format string, v ...any) {
	tfcmd.runContextInfo.logwrap.PrintlnToLocalLogs(fmt.Sprintf(format, v...))
}

// PrintlnToLogsAndStep writes to both the local operator logs and the update logs, so the
// message becomes part of the current step's SystemMessage that the end user sees
// in meshStack (in addition to the runner's local console logs).
func (tfcmd *GenericTfCmd) PrintlnToLogsAndStep(v ...any) {
	tfcmd.runContextInfo.logwrap.PrintlnToLocalAndUpdateLogs(v...)
}

func (tfcmd *GenericTfCmd) fail(err error) {
	tfcmd.failWithUserMsg(err, nil)
}

// failWithUserMsg is like fail but allows the caller to supply a pre-computed
// user-facing message (e.g. content written by a pre-run script to
// $MESHSTACK_USER_MESSAGE). When overrideUserMsg is non-nil it becomes the
// step's UserMessage instead of the generic error-derived text. The error is
// still logged to the update logs so operators can see the technical detail.
func (tfcmd *GenericTfCmd) failWithUserMsg(err error, overrideUserMsg *string) {
	tfcmd.runContextInfo.runStatus.failRunAndNotFinishedSteps()

	stepStatus := tfcmd.runContextInfo.runStatus.currentStepStatus()
	var userMsg *string
	if stepStatus == nil {
		msg := fmt.Sprintf("An error occurred: %s", err.Error())
		tfcmd.runContextInfo.logwrap.PrintlnToLocalLogs(msg)
	} else {
		// local message includes step name which is not required for the updated logs on the server.
		localMsg := fmt.Sprintf("Error in STEP [%s]: %s", stepStatus.Name, err.Error())
		tfcmd.runContextInfo.logwrap.PrintlnToLocalLogs(localMsg)

		// Build a user-facing error message. Raw process exit codes like "exit status 1"
		// are not actionable — direct users to the step logs that contain the actual command errors.
		msgText := err.Error()
		if strings.HasPrefix(msgText, "exit status ") {
			msgText = fmt.Sprintf("command failed (%s) — check the step logs above for error details.", msgText)
		}
		tfcmd.runContextInfo.logwrap.PrintlnToUpdateLogs(msgText)

		if errors.Is(tfcmd.ctx.Err(), context.DeadlineExceeded) {
			msgText = fmt.Sprintf("execution step stopped, because it exceeded the configured timeout of %d minutes", AppConfig.TfCommandTimeoutMins)
			tfcmd.runContextInfo.logwrap.PrintlnToUpdateLogs(msgText)
		} else if errors.Is(tfcmd.ctx.Err(), context.Canceled) {
			msgText = fmt.Sprintf("execution step cancelled: %s", context.Cause(tfcmd.ctx).Error())
			tfcmd.runContextInfo.logwrap.PrintlnToUpdateLogs(msgText)
		}

		if overrideUserMsg != nil {
			userMsg = overrideUserMsg
		} else {
			userMsg = &msgText
		}
	}

	tfcmd.setCurrentStepMessage(userMsg)
	tfcmd.commitStatus()
}

func (tfcmd *GenericTfCmd) createFreshCommandWd() error {
	wd := tfcmd.runContextInfo.workingDirectory

	// copy required terraform files into cmdDir
	err := tfcmd.params.source.CopyToTargetDir(wd)
	if err != nil {
		return err
	}

	tfcmd.Println("Done copying source files.")
	return nil
}

func (tfcmd *GenericTfCmd) init(tf TfFacade) error {
	tfcmd.Println("Calling tf init")
	initCtx, cancel := context.WithTimeout(tfcmd.ctx, time.Duration(AppConfig.InitTimeoutMins)*time.Minute)
	defer cancel()

	if tfcmd.runContextInfo.useMeshBackendFallback {
		exists, err := tfcmd.detectBackend()
		if err != nil {
			return err
		}
		if exists {
			tfcmd.runContextInfo.logwrap.PrintlnToLocalAndUpdateLogs("Using existing backend.")
		} else {
			tfcmd.runContextInfo.logwrap.PrintlnToLocalAndUpdateLogs("Detected no backend. Generating meshStack http backend for init.")
			if err := tfcmd.createMeshStackHttpBackendFile(); err != nil {
				return err
			}
			tfcmd.params.useWorkspaces = false // http backend does not work with workspaces
		}
	}
	return tfcmd.plainInit(initCtx, tf, false)
}

func (tfcmd *GenericTfCmd) plainInit(ctx context.Context, tf TfFacade, migrateState bool) error {

	err := tf.Init(ctx, tfexec.Upgrade(true), tfexec.ForceCopy(migrateState))
	if err != nil {
		// Wait one second and retry. It can happen, especially if a proxy is used in an environment,
		// that terraform init fails because of some connectivity issue. We therefore implement a very
		// simple retry here
		tfcmd.Printfln("Calling 'init' failed: %s", err.Error())
		time.Sleep(1000)
		tfcmd.Println("Retrying 'init' once.")
		err = tf.Init(ctx, tfexec.Upgrade(true), tfexec.ForceCopy(migrateState))
	}

	return err
}

func (tfcmd *GenericTfCmd) useWorkspaceIfNeeded(tf TfFacade) error {
	if !tfcmd.params.useWorkspaces {
		return nil
	}

	workspace, err := tfcmd.selectWorkspace(tf)

	if err != nil {
		return err
	}

	// We found an existing workspace and have selected it
	if workspace != "" {
		return nil
	}

	// if expected workspace does not exits, create it
	wsCtx, cancel := context.WithTimeout(tfcmd.ctx, time.Duration(AppConfig.WsTimeoutMins)*time.Minute)
	defer cancel()

	tfcmd.Printfln("Workspace does not exist. Creating it with suggested name: %s.", tfcmd.params.suggestedWorkspace)
	return tf.WorkspaceNew(wsCtx, tfcmd.params.suggestedWorkspace)
}

func (tfcmd *GenericTfCmd) selectWorkspace(tf TfFacade) (string, error) {
	tfcmd.Printfln("Selecting workspace: %s", tfcmd.params.suggestedWorkspace)

	wsCtx, cancelList := context.WithTimeout(tfcmd.ctx, time.Duration(AppConfig.WsTimeoutMins)*time.Minute)
	defer cancelList()

	available, current, err := tf.WorkspaceList(wsCtx)
	if err != nil {
		return "", err
	}

	// if current workspace is the expected one, return
	if strings.HasSuffix(current, tfcmd.params.buildingBlockId) {
		tfcmd.Printfln("Already on expected workspace %s.", current)
		return current, nil
	}

	// iterate over available workspaces and select the right one, if exists
	for _, ws := range available {
		if strings.HasSuffix(ws, tfcmd.params.buildingBlockId) {
			tfcmd.Printfln("Workspace %s exists, switching to it.", ws)
			err = tf.WorkspaceSelect(wsCtx, ws)
			if err != nil {
				return "", nil
			}

			return tfcmd.params.buildingBlockId, nil
		}
	}

	return "", nil
}

func (tfcmd *GenericTfCmd) deleteWorkspaceIfNeeded(tf TfFacade) {
	if !tfcmd.params.useWorkspaces {
		return
	}

	wsCtx, cancel := context.WithTimeout(tfcmd.ctx, time.Duration(AppConfig.WsTimeoutMins)*time.Minute)
	defer cancel()

	// This is primarly using the somewhat complex workspace naming logic to detect the workspace
	// that is supposed to get deleted.
	workspace, err := tfcmd.selectWorkspace(tf)

	if err != nil {
		tfcmd.Printfln("Failed to find workspace for deletion: %s, won't attempt deletion again.", err.Error())
	}

	tfcmd.Printfln("Deleting workspace: %s", workspace)

	err = tf.WorkspaceSelect(wsCtx, "default")
	if err == nil {
		err = tf.WorkspaceDelete(wsCtx, workspace, tfexec.Force(true))
	}

	if err != nil {
		tfcmd.Printfln("Failed with error: %s, won't attempt deletion again.", err.Error())
	}
}

func (tfcmd *GenericTfCmd) detectBackend() (bool, error) {
	found, file, diags := FindBackendConfig(os.DirFS(tfcmd.runContextInfo.workingDirectory))
	if diags.HasErrors() {
		return false, fmt.Errorf("errors while detecting backend config: %s", diags)
	} else if found {
		tfcmd.runContextInfo.logwrap.PrintlnToLocalAndUpdateLogs(fmt.Sprintf("Found a backend config in %s", file))
	}
	return found, nil
}

func (tfcmd *GenericTfCmd) createMeshStackHttpBackendFile() error {
	workingDirRoot, err := os.OpenRoot(tfcmd.runContextInfo.workingDirectory)
	if err != nil {
		return err
	}
	defer func() {
		_ = workingDirRoot.Close()
	}()

	backendFileName := fmt.Sprintf("meshStack_httpbackend-%d.tf", time.Now().Unix())
	if _, err := workingDirRoot.Stat(backendFileName); !errors.Is(err, fs.ErrNotExist) {
		// Consider err == nil and any other err != fs.ErrNotExist as file being present
		// It is also quite unlikely that we ever encounter this here as the backendFileName has a timestamp suffix avoiding naming collision
		return fmt.Errorf("a file with the name '%s' already exists in the working directory", backendFileName)
	}

	baseUrl := tfcmd.runContextInfo.meshstackBaseUrl
	if baseUrl == "" {
		baseUrl = AppConfig.RunApiBackend.Url
	}
	url := fmt.Sprintf(EP_State, baseUrl, tfcmd.runContextInfo.workspaceIdentifier, tfcmd.runContextInfo.bbId)

	f := hclwrite.NewEmptyFile()
	backendBlockBody := f.Body().
		AppendNewBlock("terraform", nil).
		Body().
		AppendNewBlock("backend", []string{"http"}).
		Body()
	backendBlockBody.SetAttributeValue("address", cty.StringVal(url))
	if tfcmd.runContextInfo.runToken == "" {
		// A runToken must always be present when the meshStack HTTP backend is used.
		// Both standalone (polling) and Kubernetes (single-run) modes receive the token
		// from the run object. An empty token indicates a server-side bug or an
		// incompatible old server version; failing fast avoids silently writing
		// long-lived credentials into a .tf file on disk.
		return fmt.Errorf("cannot configure meshStack HTTP backend: runToken is empty — the run object did not include an ephemeral token")
	}
	// Auth is intentionally NOT baked into this backend config (no `headers` block):
	// OpenTofu embeds the backend config verbatim into any saved plan, and a preflight
	// APPLY may apply a plan produced by an earlier DETECT run days/weeks after that
	// run's ephemeral key was revoked. Instead, the run token is supplied via
	// TF_HTTP_USERNAME/TF_HTTP_PASSWORD (see buildTfEnv), which OpenTofu re-reads fresh
	// at every plan/apply — OpenTofu's documented pattern for ephemeral backend credentials.

	tfcmd.Println(fmt.Sprintf("Writing backend config file: '%s'.", backendFileName))
	return workingDirRoot.WriteFile(backendFileName, f.Bytes(), 0600)
}

func (tfcmd *GenericTfCmd) assignOutput(tf TfFacade) {
	// make sure changes to the log file are written back to the current update status
	tfcmd.runContextInfo.logwrap.callback = func() {
		tfcmd.setCurrentStepMessage(nil)
		tfcmd.commitStatus()
	}

	// set log wrapper as output for tf lib
	tf.SetStdout(tfcmd.runContextInfo.logwrap)
	tf.SetStderr(tfcmd.runContextInfo.logwrap)
}

func (tfcmd *GenericTfCmd) collectOutput(output map[string]tfexec.OutputMeta) {

	parsedOutput := make(map[string]*TfOutput)
	for k, v := range output {
		if v.Value == nil {
			continue
		}
		outputType := matchOutputType(v)
		if outputType == DATA_TYPE_CODE || outputType == DATA_TYPE_MULTISELECT {
			jsonBytes, _ := json.MarshalIndent(&v.Value, "", "  ")
			parsedOutput[k] = &TfOutput{
				Value:     string(jsonBytes),
				Type:      outputType,
				Sensitive: v.Sensitive,
			}
		} else {
			parsedOutput[k] = &TfOutput{
				Value:     v.Value,
				Type:      outputType,
				Sensitive: v.Sensitive,
			}
		}
	}

	stepStatus := tfcmd.runContextInfo.runStatus.currentStepStatus()
	if stepStatus != nil {
		stepStatus.Outputs = parsedOutput
	}

	tfcmd.Printfln("Parsed %d output value(s).", len(parsedOutput))
}

func matchOutputType(outputMeta tfexec.OutputMeta) DataType {
	// try parse array:
	var arr []any
	err := json.Unmarshal(outputMeta.Type, &arr)

	if err != nil || len(arr) == 0 {

		// it's either a simple type or unparsable
		var str string
		_ = json.Unmarshal(outputMeta.Type, &str)

		switch {
		case str == "number":
			return DATA_TYPE_INTEGER
		case str == "bool":
			return DATA_TYPE_BOOLEAN
		case str == "string":
			return DATA_TYPE_STRING
		default:
			return DATA_TYPE_CODE
		}

	} else {
		return DATA_TYPE_CODE
	}
}

// cleanSystemEnv returns a minimal environment map containing only the
// system variables that Terraform and hook scripts need to operate correctly
// (HOME for the plugin cache, PATH for executable lookup, and temporary
// directory variables). All other variables present in the current process
// environment — e.g. credentials injected at Docker container startup — are
// intentionally excluded so they cannot leak into subprocesses.
func cleanSystemEnv() map[string]string {
	env := make(map[string]string)

	// Core system variables required for basic process operation.
	for _, key := range []string{"HOME", "PATH", "PWD", "TMPDIR", "TMP", "TEMP"} {
		if val, ok := os.LookupEnv(key); ok {
			env[key] = val
		}
	}

	// The following variables were present in the subprocess environment before the
	// explicit whitelist was introduced. They are kept here for backwards compatibility
	// so that pre-run scripts that rely on them continue to work without modification.
	for _, key := range []string{
		// SSH host key file location, set in the Docker image; required for SSH-based git sources in scripts.
		"SSH_KNOWN_HOSTS",
		// Nix package manager configuration, set in the Docker image; needed when scripts install or invoke nix packages.
		"NIX_CONFIG",
		// meshStack API base URL injected by the platform; pre-run scripts use it to call meshStack APIs.
		"MESHSTACK_ENDPOINT",
		// meshstack TF provider is also used inside BB runs (starter kits), and for smoke testing,
		// meshstack-dev sets this env to true. We need to pass this on here.
		// See https://github.com/meshcloud/terraform-provider-meshstack/blob/afb40fdecf98ac7ae3e08a937c2a9dc42e92f05e/client/client.go#L71-L71
		"MESHSTACK_SKIP_VERSION_CHECK",
		// Path to custom CA certificates, set in the Docker image; scripts that call HTTPS endpoints need this.
		"CUSTOM_CA_CERTS_PATH",
		// Terraform logging controls; operators set these to capture Terraform debug output.
		"TF_LOG", "TF_LOG_CORE", "TF_LOG_PROVIDER", "TF_LOG_PATH",
		// Disables Terraform checkpoint calls; commonly set in CI/CD environments.
		"CHECKPOINT_DISABLE",
		// Signals to Terraform that it is running in an automated pipeline (set by tfexec).
		"TF_IN_AUTOMATION",
	} {
		if val, ok := os.LookupEnv(key); ok {
			env[key] = val
		}
	}

	return env
}

// buildTfEnv constructs a clean environment map for the Terraform subprocess.
// It starts from the minimal system environment (see cleanSystemEnv) and layers
// on only the variables that the Go code explicitly configures: GIT_SSH_COMMAND
// for SSH-based source authentication and any building-block input variables
// that are marked as environment variables. No ambient process environment
// variables (e.g. Docker startup vars, cloud credentials passed to the runner
// container) are inherited.
func (tfcmd *GenericTfCmd) buildTfEnv() (map[string]string, error) {
	env := cleanSystemEnv()
	var envKeys []string

	// Set GIT_SSH_COMMAND if SSH authentication is configured
	if tfcmd.params.source != nil && tfcmd.params.source.auth.name() == AUTH_TYPE_SSH {
		gitSshCommandEnv := fmt.Sprintf("ssh -i '%s'", path.Join(tfcmd.runContextInfo.workingDirectory, TMP_FILE_SSH_CERT))
		if AppConfig.SkipHostKeyValidation {
			gitSshCommandEnv += " -o StrictHostKeyChecking=no"
		}
		tfcmd.Println(fmt.Sprintf("Setting GIT_SSH_COMMAND=%s", gitSshCommandEnv))
		env["GIT_SSH_COMMAND"] = gitSshCommandEnv
		envKeys = append(envKeys, "GIT_SSH_COMMAND")
	}

	for varName, variable := range tfcmd.params.vars {
		// Note: TF_VAR_ prefixed env vars from Building Blocks are passed in as *.auto.tfvars file (see vars() method)
		if variable.env && !strings.HasPrefix(varName, TfVarEnvPrefix) {
			val, err := variable.decryptIfSensitive(meshcrypto.Crypto)
			if err == nil {
				encodedValue, err := encodeVarValueForEnv(val, variable.Type)
				if err != nil {
					return nil, err
				}
				env[varName] = encodedValue
				envKeys = append(envKeys, varName)
			} else {
				tfcmd.Printfln("Failed to decrypt input '%s'", varName)
				tfcmd.runContextInfo.logwrap.PrintlnToUpdateLogs(fmt.Sprintf("Failed to decrypt input '%s': %s", varName, err.Error()))
				return nil, fmt.Errorf("input decryption failed for '%s'", varName)
			}
		}
	}

	// MeshStackRunTokenBasicUser is the Basic-auth username sent with the state backend
	// request. Its value is arbitrary and ignored by meshfed-api: the run JWT rides in
	// the Basic password, and meshfed-api's TfStateRunTokenBasicAuthFilter reads only the
	// password and interprets it as the token, rewriting Basic(<any-user>:<jwt>) to
	// Bearer <jwt> for the state endpoint. We send a placeholder "x" to make this explicit.
	//
	// Currently OpenTofu only supports setting basic auth for the backend, but in the future they may also support
	// setting bearer auth directly: https://github.com/opentofu/opentofu/issues/2659
	if tfcmd.runContextInfo.useMeshBackendFallback && tfcmd.runContextInfo.runToken != "" {
		env["TF_HTTP_USERNAME"] = MeshStackRunTokenBasicUser
		env["TF_HTTP_PASSWORD"] = tfcmd.runContextInfo.runToken
		// Only the key names are logged below, never the values, so listing both is safe and more debuggable.
		envKeys = append(envKeys, "TF_HTTP_USERNAME", "TF_HTTP_PASSWORD")
	}

	if len(envKeys) > 0 {
		msg := "Set the following env variables: "
		msg += strings.Join(envKeys, ", ")
		tfcmd.Println(msg)
	}

	return env, nil
}

func (tfcmd *GenericTfCmd) saveInputFiles() (savedFiles int, err error) {

	for k, v := range tfcmd.params.vars {
		if v.Type != DATA_TYPE_FILE {
			continue
		}

		if v.env {
			err := fmt.Errorf("variable '%s' with type FILE cannot be marked as environment var", k)
			tfcmd.runContextInfo.logwrap.PrintlnToLocalAndUpdateLogs(err.Error())
			return savedFiles, err
		}

		vPath := path.Join(tfcmd.runContextInfo.workingDirectory, k)
		if _, err := os.Stat(vPath); err == nil {
			tfcmd.runContextInfo.logwrap.PrintlnToLocalAndUpdateLogs(fmt.Sprintf("A file with the name '%s' already exists in the working directory and will be overwritten by a file input.", k))
		} else if !os.IsNotExist(err) {
			// There was another error while accessing the file. We just report it back as such.
			tfcmd.runContextInfo.logwrap.PrintlnToLocalAndUpdateLogs("Could not access file: " + k)
			return savedFiles, err
		}

		value, err := v.decryptIfSensitive(meshcrypto.Crypto)
		if err != nil {
			tfcmd.Printfln("Failed to decrypt file input '%s'", k)
			tfcmd.runContextInfo.logwrap.PrintlnToUpdateLogs(fmt.Sprintf("Failed to decrypt file input '%s': %s", k, err.Error()))
			return savedFiles, fmt.Errorf("file input decryption failed for '%s'", k)
		}

		tfcmd.Printfln("Writing variable file to %s", vPath)
		// FILE type values are data URLs that should be decoded directly, not encoded
		dataUrl := fmt.Sprintf("%v", value)
		fileContent, err := extractContentFromDataUrl(dataUrl)
		if err != nil {
			tfcmd.runContextInfo.logwrap.PrintlnToLocalAndUpdateLogs("Unable to decode file content from DataURL")
			return savedFiles, err
		}

		err = os.WriteFile(vPath, fileContent, 0600)

		if err != nil {
			tfcmd.runContextInfo.logwrap.PrintlnToLocalAndUpdateLogs("Unable to write variable file")
			return savedFiles, err
		}

		savedFiles++
	}
	return savedFiles, nil
}

const TfVarEnvPrefix = "TF_VAR_"

func (tfcmd *GenericTfCmd) vars() error {
	// try parsing the TF config and find 'variable' inputs
	// figuring out is optionally helpful how to deal with provided vars
	existingVariableInputs, diags := ParseVariableInputs(os.DirFS(tfcmd.runContextInfo.workingDirectory))
	if diags.HasErrors() {
		// Initialize to empty map if parsing fails - type mismatch detection will be skipped
		// for variables that could not be parsed from the Terraform configuration
		existingVariableInputs = VariableInputs{}
		_, _ = tfcmd.runContextInfo.logwrap.PrintlnToUpdateLogs(fmt.Sprintf(
			"Failed to parse Terraform config for variable inputs: %s", diags.Error(),
		))
	}

	varsFile := NewEmptyVarsFile()

	for varName, variable := range util.SortedByKeys(tfcmd.params.vars) {
		// We ignore file types because they don't belong into the env var list.
		if variable.Type == DATA_TYPE_FILE {
			continue
		}

		// pick up TF_VAR_ prefixed env variables if var key without prefix does not exist
		if variable.env && strings.HasPrefix(varName, TfVarEnvPrefix) {
			varNameTrimmed := strings.TrimPrefix(varName, TfVarEnvPrefix)
			if _, exists := tfcmd.params.vars[varNameTrimmed]; exists {
				// this makes it prefer explicitly defined input variables (not from env)
				continue
			} else {
				varName = varNameTrimmed
			}
		} else if variable.env {
			// ignore all other env vars
			continue
		}

		decryptedValue, err := variable.decryptIfSensitive(meshcrypto.Crypto)
		if err != nil {
			tfcmd.Printfln("Failed to decrypt input '%s'", varName)
			_, _ = tfcmd.runContextInfo.logwrap.PrintlnToUpdateLogs(fmt.Sprintf("Failed to decrypt input '%s': %s", varName, err.Error()))
			return fmt.Errorf("input decryption failed for '%s'", varName)
		}

		opts := AddVariableOptions{}
		stringLikeTypes := []DataType{
			DATA_TYPE_STRING,
			DATA_TYPE_SINGLESELECT,
		}
		if !slices.Contains(stringLikeTypes, variable.Type) && existingVariableInputs[varName].Type == "string" {
			// if the original data type is not string-like but the input 'variable' explicitly requires string,
			// encode the JSON value and provide it as an embedded JSON string.
			// Otherwise, it will fail anyway when Terraform reads in a value with mismatching type
			opts.EncodeAsJsonString = true
		}
		var diags hcl.Diagnostics
		// DATA_TYPE_LIST is legacy data type, should be handled same as DATA_TYPE_CODE
		if variable.Type == DATA_TYPE_CODE || variable.Type == DATA_TYPE_LIST {
			diags = varsFile.AddRawVariable(varName, fmt.Sprintf("%v", decryptedValue), opts)
		} else {
			diags = varsFile.AddVariable(varName, decryptedValue, opts)
		}
		for _, diag := range diags {
			// just log without return, as adding variable should only have warnings and very rarely errors
			_, _ = tfcmd.runContextInfo.logwrap.PrintlnToUpdateLogs(fmt.Sprintf(
				"While adding variable '%s': %s: %s", varName, diag.Summary, diag.Detail,
			))
		}
	}

	// Add meshStack-provided variables to the tfvars file.
	//
	// Run-scoped vars (run_id, run_b64) are DEPRECATED and their value is omitted on a DETECT plan
	// and on an APPLY replaying a predecessor's saved plan: applying a saved plan requires input
	// values identical to plan time, but these necessarily differ between the DETECT run and the
	// APPLY consuming its plan, so terraform would reject it with "Mismatch between input and plan
	// variable value". They are always declared but optional/nullable so omitting the value is legal
	// (reading as null in those modes); building blocks needing the full payload should read it from
	// the pre-run script's stdin (see RunScript). building_block_id is stable across runs, so it is
	// always written and stays required.
	meshStackVars := []struct {
		Name      string
		Value     string
		RunScoped bool // deprecated + optional; value only written on fresh apply/destroy
	}{
		{Name: "meshstack_building_block_id", Value: tfcmd.runContextInfo.bbId},
		{Name: "meshstack_building_block_run_b64", Value: tfcmd.runContextInfo.runJsonBase64, RunScoped: true},
		{Name: "meshstack_building_block_run_id", Value: tfcmd.runContextInfo.runId, RunScoped: true},
	}
	meshStackVarsFile := hclwrite.NewEmptyFile()
	for _, variable := range meshStackVars {
		includeRunScopedVars := tfcmd.params.runMode != DETECT.str() && tfcmd.params.planArtifactUrl == ""
		if !variable.RunScoped || includeRunScopedVars {
			diags = varsFile.AddVariable(variable.Name, variable.Value, AddVariableOptions{})
			if diags.HasErrors() {
				for _, diag := range diags {
					_, _ = tfcmd.runContextInfo.logwrap.PrintlnToUpdateLogs(fmt.Sprintf(
						"While adding variable '%s': %s: %s", variable.Name, diag.Summary, diag.Detail,
					))
				}
				continue
			}
		}
		// Declare the variable unless the building block already declares it itself.
		if _, variableBlockExists := existingVariableInputs[variable.Name]; variableBlockExists {
			tfcmd.Println(fmt.Sprintf("Skip defining variable block for %s as it is already present in configuration", variable.Name))
			continue
		}
		variableBlockBody := meshStackVarsFile.Body().AppendNewBlock("variable", []string{variable.Name}).Body()
		variableBlockBody.SetAttributeTraversal("type", hcl.Traversal{hcl.TraverseRoot{Name: "string"}})
		if variable.RunScoped {
			// Deprecated run-scoped variable: optional so it can be omitted on dry-runs and saved-plan replays.
			variableBlockBody.SetAttributeValue("nullable", cty.BoolVal(true))
			variableBlockBody.SetAttributeValue("default", cty.NullVal(cty.String))
		} else {
			variableBlockBody.SetAttributeValue("nullable", cty.BoolVal(false))
		}
	}

	// Write meshStack_run_vars.tf if it doesn't exist
	// This can be removed some day once customers have created that file manually in their repo
	// We should actually report this using metrics, but this is future work.
	if meshStackVarsFileContent := meshStackVarsFile.Bytes(); len(meshStackVarsFileContent) > 0 {
		meshStackVarsFileName := path.Join(tfcmd.runContextInfo.workingDirectory, "meshStack_run_vars.tf")
		if _, err := os.Stat(meshStackVarsFileName); os.IsNotExist(err) {

			if err := os.WriteFile(meshStackVarsFileName, meshStackVarsFile.Bytes(), 0644); err != nil {
				return fmt.Errorf("failed to write meshStack_run_vars.tf: %w", err)
			}
			tfcmd.Println("Created meshStack_run_vars.tf with meshStack-provided variable declarations")
		}
	}

	// Note: filename must end in *.auto.tfvars, otherwise it's not picked up by Terraform CLI
	// Also: Make it start with 'aaaaaa_' to give it lower precedence,
	// see https://developer.hashicorp.com/terraform/language/values/variables#variable-definition-files
	const filename = "aaaaaa_meshstack-e48f8924-a6c0-4ff0-9528-ff3c1f6f94d8.auto.tfvars"
	tfcmd.Println(fmt.Sprintf("Writing the following variables into %s: %s", filename, varsFile.VariableNames()))
	if err := os.WriteFile(path.Join(tfcmd.runContextInfo.workingDirectory, filename), varsFile.Bytes(), 0600); err != nil {
		tfcmd.runContextInfo.logwrap.PrintlnToLocalAndUpdateLogs(fmt.Sprintf("Unable to write %s file", filename))
		return fmt.Errorf("failed to write %s: %w", filename, err)
	}
	return nil
}

func encodeVarValueForEnv(value any, t DataType) (string, error) {
	// Encoding for environment variables is rather simple,
	// it's questionable if non-string env variables are really useful anymore
	// as all TF_VAR_ prefixed env vars are properly passed in via *.tfvars file now
	switch t {
	case DATA_TYPE_MULTISELECT:
		bytes, err := json.Marshal(value)
		if err != nil {
			return "", err
		} else {
			return string(bytes), nil
		}
	default:
		return fmt.Sprintf("%v", value), nil
	}
}

func extractContentFromDataUrl(dataUrl string) ([]byte, error) {
	pattern := regexp.MustCompile(`data:(.*?);base64,`)
	match := pattern.FindStringSubmatch(dataUrl)

	if len(match) != 2 {
		return nil, fmt.Errorf("no data content found in DataURL")
	}

	// Remove the whole matched prefix from the raw base64 content (starting after the comma)
	data := dataUrl[len(match[0]):]
	return base64.StdEncoding.DecodeString(data)
}

// runPreRunScript delegates to RunScript, routing the combined script output
// to the operator-visible system log and returning the user-facing message (content
// written by the script to $MESHSTACK_USER_MESSAGE) together with any execution error.
func (tfcmd *GenericTfCmd) runPreRunScript(extraEnv map[string]string) (*string, error) {
	preRunScript := tfcmd.params.preRunScript
	if preRunScript == nil || strings.TrimSpace(*preRunScript) == "" {
		tfcmd.runContextInfo.logwrap.PrintlnToLocalAndUpdateLogs("No pre-run script configured, skipping.")
		return nil, nil
	}

	tfcmd.Println("Executing pre-run script...")

	result, runErr := RunScript(tfcmd.ctx, ScriptParams{
		Name:            "pre-run",
		Script:          *preRunScript,
		TerraformBinDir: path.Join(tfcmd.bin.dir, tfcmd.params.tfVersion),
		RunMode:         tfcmd.params.runMode,
		WorkDir:         tfcmd.runContextInfo.workingDirectory,
		RunJsonBase64:   tfcmd.runContextInfo.runJsonBase64,
		ExtraEnv:        extraEnv,
	})

	var userMessage *string
	if result.UserMessage != "" {
		userMessage = &result.UserMessage
		if runErr != nil {
			tfcmd.runContextInfo.logwrap.PrintlnToLocalAndUpdateLogs(result.UserMessage)
		}
	}

	if result.SystemMessage != "" {
		tfcmd.runContextInfo.logwrap.PrintlnToLocalAndUpdateLogs(result.SystemMessage)
	}
	tfcmd.runContextInfo.logwrap.PrintlnToLocalAndUpdateLogs(fmt.Sprintf("pre-run script exited with code %d", result.ExitCode))

	return userMessage, runErr
}

func (tfcmd *GenericTfCmd) firstStep() {
	err := tfcmd.runContextInfo.runStatus.firstStep()
	if err != nil {
		tfcmd.Printfln("%s", err.Error())
	}
}

func (tfcmd *GenericTfCmd) setCurrentStepStatus(status ExecutionStatus) {
	currentStep := tfcmd.runContextInfo.runStatus.currentStepStatus()
	if currentStep != nil {
		currentStep.Status = status
	}
}

func (tfcmd *GenericTfCmd) setCurrentStepMessage(userMessage *string) {
	currentStep := tfcmd.runContextInfo.runStatus.currentStepStatus()
	if currentStep != nil {
		if userMessage != nil {
			currentStep.UserMessage = userMessage
		}

		logs := fileContentOrEmpty(tfcmd.runContextInfo.logFile_name, currentStep.LogStartIdx, tfcmd.runContextInfo.logwrap.logSize)
		currentStep.SystemMessage = &logs
	}
}

func (tfcmd *GenericTfCmd) nextStep() {
	if tfcmd.runContextInfo.asyncRun {
		return
	}

	// set status for current step
	currentStep := tfcmd.runContextInfo.runStatus.currentStepStatus()
	if currentStep != nil {
		currentStep.Status = SUCCEEDED
	}

	// progress
	err := tfcmd.runContextInfo.runStatus.nextStep()
	if err != nil {
		tfcmd.Printfln("Cannot set next step. Implementation error.")
	} else {
		tfcmd.runContextInfo.runStatus.currentStepStatus().Status = IN_PROGRESS

		// next step starts logging here:
		tfcmd.runContextInfo.runStatus.currentStepStatus().LogStartIdx = tfcmd.runContextInfo.logwrap.logSize
	}
}

func (tfcmd *GenericTfCmd) setRunStatus(e ExecutionStatus) {
	tfcmd.runContextInfo.runStatus.Status = e
	tfcmd.Printfln("STATUS %s", e.str())
}

func (tfcmd *GenericTfCmd) commitStatus() {
	tfcmd.runContextInfo.reportStatus = *(tfcmd.runContextInfo.runStatus)
}

// startRun activates the first step and marks the overall run as IN_PROGRESS.
// Every execute() implementation must call this exactly once before any work begins.
func (tfcmd *GenericTfCmd) startRun() {
	tfcmd.firstStep()
	tfcmd.setRunStatus(IN_PROGRESS)
	tfcmd.commitStatus()
}

// advanceStep finalizes the current step (capturing its logs and optional userMessage)
// and transitions to the next step. commitStatus is included.
func (tfcmd *GenericTfCmd) advanceStep(userMessage *string) {
	tfcmd.setCurrentStepMessage(userMessage) // snapshot logs (+ optional user message) for the completed step
	tfcmd.nextStep()                         // mark it SUCCEEDED, advance pointer, mark next IN_PROGRESS
	tfcmd.commitStatus()
}

// completeRun marks the current (last) step and the overall run as SUCCEEDED and
// persists the status. Every execute() implementation must call this as its final action.
func (tfcmd *GenericTfCmd) completeRun(userMessage *string) {
	tfcmd.setCurrentStepMessage(userMessage)
	tfcmd.setCurrentStepStatus(SUCCEEDED)
	tfcmd.setRunStatus(SUCCEEDED)
	tfcmd.commitStatus()
}
