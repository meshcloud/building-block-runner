package crypto

import (
	"fmt"
	"slices"

	"github.com/meshcloud/meshstack-cli/client"
)

// DecryptRun decrypts the sensitive inputs and the secret of the implementation.
func (c *Crypto) DecryptRun(run client.MeshRunnerRun) (client.MeshRunnerRun, error) {
	run.Spec.BuildingBlock.Spec.Inputs = slices.Clone(run.Spec.BuildingBlock.Spec.Inputs)
	for i := range run.Spec.BuildingBlock.Spec.Inputs {
		input := &run.Spec.BuildingBlock.Spec.Inputs[i]
		// meshStack encrypts only strings.
		if value, isString := input.Value.(string); input.IsSensitive && isString {
			if err := c.decryptInPlace(&value); err != nil {
				return client.MeshRunnerRun{}, fmt.Errorf("sensitive input %s: %w", input.Key, err)
			}
			input.Value = value
		}
	}
	if err := c.decryptImplementation(&run.Spec.Definition.Spec.Implementation); err != nil {
		return client.MeshRunnerRun{}, fmt.Errorf("implementation: %w", err)
	}
	return run, nil
}

func (c *Crypto) decryptImplementation(implementation *client.MeshRunnerRunImplementation) error {
	switch {
	case implementation.Terraform != nil:
		terraform := *implementation.Terraform
		implementation.Terraform = &terraform
		if terraform.SshPrivateKey == nil {
			return nil
		}
		terraform.SshPrivateKey = new(*terraform.SshPrivateKey)
		return c.decryptInPlace(terraform.SshPrivateKey)
	case implementation.GithubWorkflow != nil:
		github := *implementation.GithubWorkflow
		implementation.GithubWorkflow = &github
		return c.decryptInPlace(&github.AppPem)
	case implementation.GitlabCiCd != nil:
		gitlab := *implementation.GitlabCiCd
		implementation.GitlabCiCd = &gitlab
		return c.decryptInPlace(&gitlab.PipelineTriggerToken)
	case implementation.AzureDevops != nil:
		azureDevops := *implementation.AzureDevops
		implementation.AzureDevops = &azureDevops
		return c.decryptInPlace(&azureDevops.PersonalAccessToken)
	}
	return nil
}

func (c *Crypto) decryptInPlace(secret *string) error {
	if *secret == "" {
		return nil
	}
	decrypted, err := c.Decrypt(*secret)
	if err != nil {
		return err
	}
	*secret = decrypted
	return nil
}
