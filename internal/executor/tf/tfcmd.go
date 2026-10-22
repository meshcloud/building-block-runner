package tf

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/client/types/enum"
	"github.com/meshcloud/meshstack-cli/pkg/setting"
	"github.com/zclconf/go-cty/cty"

	tfconfig "github.com/meshcloud/building-block-runner/internal/config/tf"
	"github.com/meshcloud/building-block-runner/internal/executor"
)

const (
	stateEndpointFormat = "%s/api/terraform/state/workspace/%s/buildingBlock/%s"

	initFailedHint = "Check provider and / or backend config"

	// MeshStackRunTokenBasicUser is ignored by meshfed-api, which reads the run token from the Basic password.
	MeshStackRunTokenBasicUser = "x"
)

// stateLockTimeout lets a command wait for a state lock that a `meshstack bb tfstate exec` session or another run
// holds. Init gets none, as tfexec passes -lock-timeout to init only for Terraform before 0.15.
func stateLockTimeout() *tfexec.LockTimeoutOption {
	return tfexec.LockTimeout("5m")
}

type GenericCmd struct {
	ctx                  context.Context //nolint:containedctx // a command lives for one run and is cancelled with it.
	run                  run
	cfg                  tfconfig.Config
	source               GitSource
	moduleDir            string
	useWorkspaces        bool
	keycloakWifTokenFile string
	bin                  *Binaries
	reporter             *executor.Reporter
	steps                tfSteps
}

func (tfcmd *GenericCmd) tfVersion() string {
	return tfcmd.run.terraform().TerraformVersion
}

func (tfcmd *GenericCmd) planFile() string {
	return path.Join(tfcmd.moduleDir, "plan.tfplan")
}

type Cmd interface {
	execute() error
}

func (tfcmd *GenericCmd) withInitTimeout(ctx context.Context, what string) (context.Context, context.CancelFunc) {
	timeout := tfcmd.cfg.InitTimeout.Duration
	return context.WithTimeoutCause(ctx, timeout, fmt.Errorf("%s exceeded tf.initTimeout of %v", what, timeout))
}

func timedOut(ctx context.Context, step string, err error) error {
	if err == nil || !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("%w during %s: %w", context.Cause(ctx), step, err)
}

func (tfcmd *GenericCmd) init(ctx context.Context, tf Facade) error {
	slog.InfoContext(ctx, "running tf init", "version", tfcmd.tfVersion())
	if tfcmd.run.terraform().UseMeshHttpBackendFallback {
		exists, err := tfcmd.detectBackend()
		if err != nil {
			return err
		}
		if !exists {
			tfcmd.steps.initTf.Logf("Found no backend config, so the state of building block %s goes to the meshStack http backend.", tfcmd.run.Spec.BuildingBlock.Uuid)
			if err := tfcmd.createMeshStackHttpBackendFile(); err != nil {
				return err
			}
			tfcmd.useWorkspaces = false // the http backend has no workspaces
		}
	}
	return timedOut(ctx, "tf init", tf.Init(ctx, tfexec.Upgrade(true)))
}

func (tfcmd *GenericCmd) useWorkspaceIfNeeded(ctx context.Context, tf Facade) error {
	if !tfcmd.useWorkspaces {
		return nil
	}

	workspace, err := tfcmd.selectWorkspace(ctx, tf)
	if err != nil {
		return err
	}

	if workspace != "" {
		return nil
	}

	slog.InfoContext(ctx, "creating the workspace", "workspace", tfcmd.run.workspaceName())
	return timedOut(ctx, "tf workspace new", tf.WorkspaceNew(ctx, tfcmd.run.workspaceName(), stateLockTimeout()))
}

func (tfcmd *GenericCmd) selectWorkspace(ctx context.Context, tf Facade) (string, error) {
	slog.InfoContext(ctx, "selecting the workspace", "workspace", tfcmd.run.workspaceName())

	available, current, err := tf.WorkspaceList(ctx)
	if err != nil {
		return "", timedOut(ctx, "tf workspace list", err)
	}

	if strings.HasSuffix(current, tfcmd.run.Spec.BuildingBlock.Uuid) {
		slog.InfoContext(ctx, "already on the workspace", "workspace", current)
		return current, nil
	}

	for _, ws := range available {
		if strings.HasSuffix(ws, tfcmd.run.Spec.BuildingBlock.Uuid) {
			slog.InfoContext(ctx, "switching to the existing workspace", "workspace", ws)
			if err := tf.WorkspaceSelect(ctx, ws); err != nil {
				return "", fmt.Errorf("select the workspace %s: %w", ws, timedOut(ctx, "tf workspace select", err))
			}

			return tfcmd.run.Spec.BuildingBlock.Uuid, nil
		}
	}

	return "", nil
}

func (tfcmd *GenericCmd) deleteWorkspaceIfNeeded(tf Facade) {
	if !tfcmd.useWorkspaces {
		return
	}

	ctx, cancel := tfcmd.withInitTimeout(tfcmd.ctx, "the workspace delete")
	defer cancel()

	workspace, err := tfcmd.selectWorkspace(ctx, tf)
	if err != nil {
		slog.WarnContext(ctx, "finding the workspace to delete failed", "error", err)
	}

	slog.InfoContext(ctx, "deleting the workspace", "workspace", workspace)

	err = tf.WorkspaceSelect(ctx, "default")
	if err == nil {
		err = tf.WorkspaceDelete(ctx, workspace, tfexec.Force(true), stateLockTimeout())
	}

	if err != nil {
		slog.WarnContext(ctx, "deleting the workspace failed", "workspace", workspace, "error", timedOut(ctx, "tf workspace delete", err))
	}
}

func (tfcmd *GenericCmd) detectBackend() (bool, error) {
	found, file, diags := FindBackendConfig(os.DirFS(tfcmd.moduleDir))
	if diags.HasErrors() {
		return false, fmt.Errorf("errors while detecting backend config: %w", diags)
	} else if found {
		tfcmd.steps.initTf.Logf("Using the backend configured in %s.", file)
	}
	return found, nil
}

func (tfcmd *GenericCmd) createMeshStackHttpBackendFile() error {
	workingDirRoot, err := os.OpenRoot(tfcmd.moduleDir)
	if err != nil {
		return err
	}
	defer func() {
		_ = workingDirRoot.Close()
	}()

	backendFileName := fmt.Sprintf("meshStack_httpbackend-%d.tf", time.Now().Unix())
	if _, err := workingDirRoot.Stat(backendFileName); !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("a file with the name '%s' already exists in the working directory", backendFileName)
	}

	baseUrl := cmp.Or(tfcmd.run.Links.MeshstackBaseUrl.Href, os.Getenv(setting.Endpoint.EnvKey()))
	url := fmt.Sprintf(stateEndpointFormat, baseUrl, tfcmd.run.Spec.BuildingBlock.Spec.WorkspaceIdentifier, tfcmd.run.Spec.BuildingBlock.Uuid)

	f := hclwrite.NewEmptyFile()
	backendBlockBody := f.Body().
		AppendNewBlock("terraform", nil).
		Body().
		AppendNewBlock("backend", []string{"http"}).
		Body()
	backendBlockBody.SetAttributeValue("address", cty.StringVal(url))
	if lockUrl := tfcmd.run.Links.TfStateLock.Href; lockUrl != "" {
		backendBlockBody.SetAttributeValue("lock_address", cty.StringVal(lockUrl))
		backendBlockBody.SetAttributeValue("lock_method", cty.StringVal(http.MethodPost))
		backendBlockBody.SetAttributeValue("unlock_address", cty.StringVal(lockUrl))
		backendBlockBody.SetAttributeValue("unlock_method", cty.StringVal(http.MethodDelete))
	}
	// tofu gets the token in TF_HTTP_PASSWORD, as a saved plan embeds the backend config and may be applied after the token expired.
	if tfcmd.run.Spec.RunToken == "" {
		return errors.New("cannot configure meshStack HTTP backend: runToken is empty — the run object did not include an ephemeral token")
	}
	slog.InfoContext(tfcmd.ctx, "writing the backend config", "file", backendFileName)
	return workingDirRoot.WriteFile(backendFileName, f.Bytes(), 0o600)
}

func (tfcmd *GenericCmd) collectOutputs(tf Facade) error {
	tf.SetStdout(io.Discard)
	tf.SetStderr(tfcmd.steps.output)
	output, err := tf.Output(tfcmd.ctx)
	if err != nil {
		return err
	}

	parsedOutput := make(map[string]client.MeshRunnerRunOutput)
	for k, v := range output {
		if v.Value == nil {
			continue
		}
		if v.Sensitive {
			tfcmd.steps.output.Logf("WARN: output %q is sensitive, but meshStack shows building block outputs in clear text. Store the secret in a vault and output a reference to it.", k)
		}
		outputType := matchOutputType(v)
		var value any = v.Value
		if outputType == client.MeshBuildingBlockIOTypeCode {
			jsonBytes, err := json.MarshalIndent(&v.Value, "", "  ")
			if err != nil {
				return fmt.Errorf("output %s: %w", k, err)
			}
			value = string(jsonBytes)
		}
		parsedOutput[k] = client.MeshRunnerRunOutput{Value: value, Type: outputType.String(), Sensitive: v.Sensitive}
	}

	tfcmd.steps.output.SetOutputs(parsedOutput)

	slog.InfoContext(tfcmd.ctx, "collected the outputs", "count", len(parsedOutput))
	return nil
}

func matchOutputType(outputMeta tfexec.OutputMeta) enum.Entry[client.MeshBuildingBlockIOType] {
	var arr []any
	if err := json.Unmarshal(outputMeta.Type, &arr); err == nil && len(arr) > 0 {
		return client.MeshBuildingBlockIOTypeCode
	}
	var str string
	_ = json.Unmarshal(outputMeta.Type, &str)
	switch str {
	case "number":
		return client.MeshBuildingBlockIOTypeInteger
	case "bool":
		return client.MeshBuildingBlockIOTypeBoolean
	case "string":
		return client.MeshBuildingBlockIOTypeString
	default:
		return client.MeshBuildingBlockIOTypeCode
	}
}

var tfSystemEnv = slices.Concat(executor.InheritedEnv, []string{"PWD", "MESHSTACK_ENDPOINT"})

func cleanSystemEnv() map[string]string {
	env := make(map[string]string)
	for _, key := range tfSystemEnv {
		if val, ok := os.LookupEnv(key); ok {
			env[key] = val
		}
	}
	return env
}

func (tfcmd *GenericCmd) registryRetryEnv() map[string]string {
	retries := strconv.Itoa(tfcmd.cfg.InitRetries)
	return map[string]string{
		"TF_REGISTRY_DISCOVERY_RETRY": retries,
		"TF_PROVIDER_DOWNLOAD_RETRY":  retries,
		"TF_REGISTRY_CLIENT_TIMEOUT":  "30",
	}
}

// About 5 minutes of 1, 2, 4, 8, 16, then 30 s waits, longer than a meshStack deployment.
const stateBackendRetryMax = "14"

func (tfcmd *GenericCmd) buildTfEnv() (map[string]string, error) {
	env := cleanSystemEnv()
	maps.Copy(env, tfcmd.registryRetryEnv())
	env["TF_HTTP_RETRY_MAX"] = stateBackendRetryMax
	var envKeys []string

	gitEnv := tfcmd.source.gitEnv()
	maps.Copy(env, gitEnv)
	envKeys = slices.AppendSeq(envKeys, maps.Keys(gitEnv))

	for _, input := range tfcmd.run.Spec.BuildingBlock.Spec.Inputs {
		// vars() writes the TF_VAR_ inputs to the tfvars file.
		if input.Env && !strings.HasPrefix(input.Key, VarEnvPrefix) {
			encodedValue, err := encodeVarValueForEnv(input)
			if err != nil {
				return nil, err
			}
			env[input.Key] = encodedValue
			envKeys = append(envKeys, input.Key)
		}
	}

	// OpenTofu has no bearer auth for the http backend: https://github.com/opentofu/opentofu/issues/2659
	if tfcmd.run.terraform().UseMeshHttpBackendFallback && tfcmd.run.Spec.RunToken != "" {
		env["TF_HTTP_USERNAME"] = MeshStackRunTokenBasicUser
		env["TF_HTTP_PASSWORD"] = tfcmd.run.Spec.RunToken
		envKeys = append(envKeys, "TF_HTTP_USERNAME", "TF_HTTP_PASSWORD")
	}

	if len(envKeys) > 0 {
		slog.InfoContext(tfcmd.ctx, "setting env variables", "names", envKeys)
	}

	return env, nil
}

func (tfcmd *GenericCmd) saveInputFiles() (savedFiles int, err error) {
	for _, input := range tfcmd.run.Spec.BuildingBlock.Spec.Inputs {
		if input.Type != client.MeshBuildingBlockIOTypeFile {
			continue
		}

		if input.Env {
			err := fmt.Errorf("variable '%s' with type FILE cannot be marked as environment var", input.Key)
			tfcmd.steps.input.Logf("%v", err)
			return savedFiles, err
		}

		vPath := path.Join(tfcmd.moduleDir, input.Key)
		if _, err := os.Stat(vPath); err == nil {
			tfcmd.steps.input.Logf("A file with the name '%s' already exists in the working directory and will be overwritten by a file input.", input.Key)
		} else if !os.IsNotExist(err) {
			tfcmd.steps.input.Logf("Could not access the file input %s: %v", input.Key, err)
			return savedFiles, err
		}

		slog.InfoContext(tfcmd.ctx, "writing a file input", "file", vPath)
		dataUrl := fmt.Sprintf("%v", input.Value)
		fileContent, err := extractContentFromDataUrl(dataUrl)
		if err != nil {
			tfcmd.steps.input.Logf("Could not decode the data URL of the file input %s: %v", input.Key, err)
			return savedFiles, err
		}

		err = os.WriteFile(vPath, fileContent, 0o600)
		if err != nil {
			tfcmd.steps.input.Logf("Could not write the file input %s: %v", input.Key, err)
			return savedFiles, err
		}

		savedFiles++
	}
	return savedFiles, nil
}

const VarEnvPrefix = "TF_VAR_"

func (tfcmd *GenericCmd) vars() error {
	existingVariableInputs, diags := ParseVariableInputs(os.DirFS(tfcmd.moduleDir))
	if diags.HasErrors() {
		existingVariableInputs = VariableInputs{}
		tfcmd.steps.input.Logf(
			"Failed to parse Terraform config for variable inputs: %s", diags.Error(),
		)
	}

	varsFile := NewEmptyVarsFile()

	inputs := tfcmd.run.inputsByKey()
	for _, varName := range slices.Sorted(maps.Keys(inputs)) {
		input := inputs[varName]
		if input.Type == client.MeshBuildingBlockIOTypeFile {
			continue
		}

		if input.Env && strings.HasPrefix(varName, VarEnvPrefix) {
			varNameTrimmed := strings.TrimPrefix(varName, VarEnvPrefix)
			if _, exists := inputs[varNameTrimmed]; exists {
				continue
			}
			varName = varNameTrimmed
		} else if input.Env {
			continue
		}

		opts := AddVariableOptions{}
		stringLikeTypes := []enum.Entry[client.MeshBuildingBlockIOType]{
			client.MeshBuildingBlockIOTypeString,
			client.MeshBuildingBlockIOTypeSingleSelect,
		}
		if !slices.Contains(stringLikeTypes, input.Type) && existingVariableInputs[varName].Type == "string" {
			// tofu rejects any other value for a variable declared as string.
			opts.EncodeAsJsonString = true
		}
		var addDiags hcl.Diagnostics
		if input.Type == client.MeshBuildingBlockIOTypeCode || input.Type == client.MeshBuildingBlockIOTypeList {
			addDiags = varsFile.AddRawVariable(varName, fmt.Sprintf("%v", input.Value), opts)
		} else {
			addDiags = varsFile.AddVariable(varName, input.Value, opts)
		}
		for _, diag := range addDiags {
			tfcmd.steps.input.Logf(
				"While adding variable '%s': %s: %s", varName, diag.Summary, diag.Detail,
			)
		}
	}

	// A saved plan applies only with the input values it was planned with, so the deprecated run-scoped
	// variables get no value on a DETECT run or a plan replay.
	meshStackVars := []struct {
		Name      string
		Value     string
		RunScoped bool
		Nullable  bool
	}{
		{Name: "meshstack_building_block_id", Value: tfcmd.run.Spec.BuildingBlock.Uuid},
		{Name: "meshstack_building_block_run_b64", Value: tfcmd.run.jsonBase64(), RunScoped: true, Nullable: true},
		{Name: "meshstack_building_block_run_id", Value: tfcmd.run.Metadata.Uuid.String(), RunScoped: true, Nullable: true},
		{Name: "meshstack_keycloak_wif_token_file", Value: tfcmd.keycloakWifTokenFile, Nullable: true},
	}
	meshStackVarsFile := hclwrite.NewEmptyFile()
	for _, variable := range meshStackVars {
		includeRunScopedVars := tfcmd.run.behavior != behaviorDetect && !tfcmd.run.approvedPlan()
		omitted := variable.RunScoped && !includeRunScopedVars || variable.Nullable && variable.Value == ""
		if !omitted {
			diags = varsFile.AddVariable(variable.Name, variable.Value, AddVariableOptions{})
			if diags.HasErrors() {
				for _, diag := range diags {
					tfcmd.steps.input.Logf(
						"While adding variable '%s': %s: %s", variable.Name, diag.Summary, diag.Detail,
					)
				}
				continue
			}
		}
		if _, variableBlockExists := existingVariableInputs[variable.Name]; variableBlockExists {
			slog.InfoContext(tfcmd.ctx, "the building block declares the variable itself", "variable", variable.Name)
			continue
		}
		variableBlockBody := meshStackVarsFile.Body().AppendNewBlock("variable", []string{variable.Name}).Body()
		variableBlockBody.SetAttributeTraversal("type", hcl.Traversal{hcl.TraverseRoot{Name: "string"}})
		if variable.Nullable {
			variableBlockBody.SetAttributeValue("nullable", cty.BoolVal(true))
			variableBlockBody.SetAttributeValue("default", cty.NullVal(cty.String))
		} else {
			variableBlockBody.SetAttributeValue("nullable", cty.BoolVal(false))
		}
	}

	if meshStackVarsFileContent := meshStackVarsFile.Bytes(); len(meshStackVarsFileContent) > 0 {
		meshStackVarsFileName := path.Join(tfcmd.moduleDir, "meshStack_run_vars.tf")
		if _, err := os.Stat(meshStackVarsFileName); os.IsNotExist(err) {
			if err := os.WriteFile(meshStackVarsFileName, meshStackVarsFile.Bytes(), 0o600); err != nil {
				return fmt.Errorf("failed to write meshStack_run_vars.tf: %w", err)
			}
			slog.InfoContext(tfcmd.ctx, "declared the meshStack variables", "file", meshStackVarsFileName)
		}
	}

	// tofu reads only *.auto.tfvars by itself, and the aaaaaa_ prefix gives these values the lowest precedence.
	const filename = "aaaaaa_meshstack-e48f8924-a6c0-4ff0-9528-ff3c1f6f94d8.auto.tfvars"
	slog.InfoContext(tfcmd.ctx, "writing the variables", "file", filename, "names", varsFile.VariableNames())
	if err := os.WriteFile(path.Join(tfcmd.moduleDir, filename), varsFile.Bytes(), 0o600); err != nil {
		tfcmd.steps.input.Logf("Could not write the variables to %s: %v", filename, err)
		return fmt.Errorf("failed to write %s: %w", filename, err)
	}
	return nil
}

func encodeVarValueForEnv(input client.MeshRunnerRunInput) (string, error) {
	if input.Type == client.MeshBuildingBlockIOTypeMultiSelect {
		bytes, err := json.Marshal(input.Value)
		if err != nil {
			return "", err
		}
		return string(bytes), nil
	}
	return fmt.Sprintf("%v", input.Value), nil
}

func extractContentFromDataUrl(dataUrl string) ([]byte, error) {
	mediaTypeAndData, isDataUrl := strings.CutPrefix(dataUrl, "data:")
	_, data, isBase64 := strings.Cut(mediaTypeAndData, ";base64,")
	if !isDataUrl || !isBase64 {
		return nil, errors.New("no data content found in DataURL")
	}
	return base64.StdEncoding.DecodeString(data)
}

func (tfcmd *GenericCmd) runPreRunScript(extraEnv map[string]string) (userMessage string, err error) {
	preRunScript := tfcmd.run.terraform().PreRunScript
	if preRunScript == nil || strings.TrimSpace(*preRunScript) == "" {
		tfcmd.steps.preRunScript.Logf("No pre-run script configured, skipping.")
		return "", nil
	}

	slog.InfoContext(tfcmd.ctx, "running the pre-run script", "runMode", string(tfcmd.run.behavior))

	result, err := RunScript(tfcmd.ctx, ScriptParams{
		Name:            "pre-run",
		Script:          *preRunScript,
		TerraformBinDir: path.Join(tfcmd.bin.dir, tfcmd.tfVersion()),
		RunMode:         string(tfcmd.run.behavior),
		WorkDir:         tfcmd.moduleDir,
		RunJson:         tfcmd.run.Json,
		ExtraEnv:        extraEnv,
	})
	if err != nil {
		return "", err
	}

	if result.UserMessage != "" && result.Failure != nil {
		tfcmd.steps.preRunScript.Logf("%s", result.UserMessage)
	}

	if result.SystemMessage != "" {
		tfcmd.steps.preRunScript.Logf("%s", result.SystemMessage)
	}
	tfcmd.steps.preRunScript.Logf("pre-run script exited with code %d", result.ExitCode)

	return result.UserMessage, result.Failure
}
