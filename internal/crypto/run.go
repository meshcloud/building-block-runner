package crypto

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"slices"

	"github.com/meshcloud/meshstack-cli/client"
)

var implementationSecretKeys = map[client.MeshRunnerImplementationType]string{
	client.MeshRunnerImplementationTypeTerraform:      "sshPrivateKey",
	client.MeshRunnerImplementationTypeGithubWorkflow: "appPem",
	client.MeshRunnerImplementationTypeGitlabCiCd:     "pipelineTriggerToken",
	client.MeshRunnerImplementationTypeAzureDevops:    "personalAccessToken",
}

type secretMapper func(secret jsontext.Value) (jsontext.Value, error)

// DecryptRun decrypts the sensitive inputs and the secret of the implementation.
func (c *Crypto) DecryptRun(run client.MeshRunnerRun) (client.MeshRunnerRun, error) {
	return mapSecrets(run, c.decryptString)
}

var redacted = jsontext.Value(`"<redacted>"`)

// RedactRun hides the run token and every value that DecryptRun decrypts.
func RedactRun(run client.MeshRunnerRun) (client.MeshRunnerRun, error) {
	if run.Spec.RunToken != "" {
		run.Spec.RunToken = "<redacted>"
	}
	return mapSecrets(run, func(jsontext.Value) (jsontext.Value, error) {
		return redacted, nil
	})
}

func mapSecrets(run client.MeshRunnerRun, mapSecret secretMapper) (client.MeshRunnerRun, error) {
	run.Spec.BuildingBlock.Spec.Inputs = slices.Clone(run.Spec.BuildingBlock.Spec.Inputs)
	for i, input := range run.Spec.BuildingBlock.Spec.Inputs {
		if !input.IsSensitive {
			continue
		}
		mapped, err := mapSecret(input.Value)
		if err != nil {
			return client.MeshRunnerRun{}, fmt.Errorf("sensitive input %s: %w", input.Key, err)
		}
		run.Spec.BuildingBlock.Spec.Inputs[i].Value = mapped
	}

	implementation, err := mapImplementationSecret(run.Spec.Definition.Spec, mapSecret)
	if err != nil {
		return client.MeshRunnerRun{}, err
	}
	run.Spec.Definition.Spec.Implementation = implementation
	return run, nil
}

func mapImplementationSecret(definition client.MeshRunnerRunDefinitionSpec, mapSecret secretMapper) (jsontext.Value, error) {
	implementationType, err := definition.ImplementationType()
	if err != nil {
		return nil, err
	}
	secretKey, hasSecret := implementationSecretKeys[implementationType]
	if !hasSecret {
		return definition.Implementation, nil
	}
	var fields map[string]jsontext.Value
	if err = json.Unmarshal(definition.Implementation, &fields); err != nil {
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
func (c *Crypto) decryptString(value jsontext.Value) (jsontext.Value, error) {
	if value.Kind() != '"' {
		return value, nil
	}
	var encrypted string
	if err := json.Unmarshal(value, &encrypted); err != nil {
		return nil, err
	}
	if encrypted == "" {
		return value, nil
	}
	decrypted, err := c.Decrypt(encrypted)
	if err != nil {
		return nil, err
	}
	return json.Marshal(decrypted)
}
