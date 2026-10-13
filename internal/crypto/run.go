package crypto

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"slices"

	"github.com/meshcloud/building-block-runner/internal/http/meshapi"
)

var implementationSecretKeys = map[meshapi.MeshRunnerImplementationType]string{
	meshapi.MeshRunnerImplementationTypeTerraform:      "sshPrivateKey",
	meshapi.MeshRunnerImplementationTypeGithubWorkflow: "appPem",
	meshapi.MeshRunnerImplementationTypeGitlabCiCd:     "pipelineTriggerToken",
	meshapi.MeshRunnerImplementationTypeAzureDevops:    "personalAccessToken",
}

type secretMapper func(secret jsontext.Value) (jsontext.Value, error)

// DecryptRun decrypts the sensitive inputs and the secret of the implementation.
func (c Crypto) DecryptRun(run meshapi.MeshRunnerRun) (meshapi.MeshRunnerRun, error) {
	return mapSecrets(run, c.decryptString)
}

var redacted = jsontext.Value(`"<redacted>"`)

// RedactRun hides the run token and every value that DecryptRun decrypts.
func RedactRun(run meshapi.MeshRunnerRun) (meshapi.MeshRunnerRun, error) {
	if run.Spec.RunToken != "" {
		run.Spec.RunToken = "<redacted>"
	}
	return mapSecrets(run, func(jsontext.Value) (jsontext.Value, error) {
		return redacted, nil
	})
}

func mapSecrets(run meshapi.MeshRunnerRun, mapSecret secretMapper) (meshapi.MeshRunnerRun, error) {
	run.Spec.BuildingBlock.Spec.Inputs = slices.Clone(run.Spec.BuildingBlock.Spec.Inputs)
	for i, input := range run.Spec.BuildingBlock.Spec.Inputs {
		if !input.IsSensitive {
			continue
		}
		mapped, err := mapSecret(input.Value)
		if err != nil {
			return meshapi.MeshRunnerRun{}, fmt.Errorf("sensitive input %s: %w", input.Key, err)
		}
		run.Spec.BuildingBlock.Spec.Inputs[i].Value = mapped
	}

	implementation, err := mapImplementationSecret(run.Spec.Definition.Spec, mapSecret)
	if err != nil {
		return meshapi.MeshRunnerRun{}, err
	}
	run.Spec.Definition.Spec.Implementation = implementation
	return run, nil
}

func mapImplementationSecret(definition meshapi.MeshRunnerRunDefinitionSpec, mapSecret secretMapper) (jsontext.Value, error) {
	implementationType, err := definition.ImplementationType()
	if err != nil {
		return nil, err
	}
	secretKey, hasSecret := implementationSecretKeys[implementationType]
	if !hasSecret {
		return definition.Implementation, nil
	}
	var fields map[string]jsontext.Value
	if err := json.Unmarshal(definition.Implementation, &fields); err != nil {
		return nil, fmt.Errorf("implementation: %w", err)
	}
	secret, ok := fields[secretKey]
	if !ok {
		return definition.Implementation, nil
	}
	if fields[secretKey], err = mapSecret(secret); err != nil {
		return nil, fmt.Errorf("implementation %s: %w", secretKey, err)
	}
	return json.Marshal(fields, json.Deterministic(true))
}

// decryptString leaves a value that is no string or empty as it is, as meshStack encrypts only strings.
func (c Crypto) decryptString(value jsontext.Value) (jsontext.Value, error) {
	var encrypted string
	if json.Unmarshal(value, &encrypted) != nil || encrypted == "" {
		return value, nil
	}
	decrypted, err := c.Decrypt(encrypted)
	if err != nil {
		return nil, err
	}
	return json.Marshal(decrypted)
}
