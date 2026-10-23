package crypto

import (
	"math/big"
	"testing"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_RunSecrets_AreDecrypted(t *testing.T) {
	key := generateKey(t)
	c := Crypto{PrivateKeys: PrivateKeys{{key}}}
	encrypted := func(plainText string) string {
		return encryptLikeMeshStack(t, key, plainText)
	}
	runWith := func(implementation client.MeshRunnerRunImplementation, inputs ...client.MeshRunnerRunInput) client.MeshRunnerRun {
		var run client.MeshRunnerRun
		run.Spec.RunToken = "a-run-token"
		run.Spec.BuildingBlock.Spec.Inputs = inputs
		run.Spec.Definition.Spec.Implementation = implementation
		return run
	}
	manual := client.MeshRunnerRunImplementation{Type: client.MeshRunnerImplementationTypeManual, Manual: &client.MeshRunnerManualImplementation{}}

	t.Run("DecryptRun decrypts sensitive string inputs only", func(t *testing.T) {
		run := runWith(manual,
			client.MeshRunnerRunInput{Key: "secret", Value: encrypted("plain"), IsSensitive: true},
			client.MeshRunnerRunInput{Key: "number", Value: big.NewInt(42), IsSensitive: true},
			client.MeshRunnerRunInput{Key: "empty", Value: "", IsSensitive: true},
			client.MeshRunnerRunInput{Key: "public", Value: "as is"},
		)

		decrypted, err := c.DecryptRun(run)

		require.NoError(t, err)
		var values []any
		for _, input := range decrypted.Spec.BuildingBlock.Spec.Inputs {
			values = append(values, input.Value)
		}
		assert.Equal(t, []any{"plain", big.NewInt(42), "", "as is"}, values)
		assert.NotEqual(t, run.Spec.BuildingBlock.Spec.Inputs[0].Value, decrypted.Spec.BuildingBlock.Spec.Inputs[0].Value)
	})

	t.Run("DecryptRun decrypts the secret of each implementation", func(t *testing.T) {
		run := runWith(client.MeshRunnerRunImplementation{Terraform: &client.MeshRunnerTerraformImplementation{SshPrivateKey: new(encrypted("key"))}})
		decrypted, err := c.DecryptRun(run)
		require.NoError(t, err)
		assert.Equal(t, "key", *decrypted.Spec.Definition.Spec.Implementation.Terraform.SshPrivateKey)
		assert.NotEqual(t, "key", *run.Spec.Definition.Spec.Implementation.Terraform.SshPrivateKey)

		run = runWith(client.MeshRunnerRunImplementation{GithubWorkflow: &client.MeshRunnerGithubImplementation{AppPem: encrypted("pem")}})
		decrypted, err = c.DecryptRun(run)
		require.NoError(t, err)
		assert.Equal(t, "pem", decrypted.Spec.Definition.Spec.Implementation.GithubWorkflow.AppPem)
		assert.NotEqual(t, "pem", run.Spec.Definition.Spec.Implementation.GithubWorkflow.AppPem)

		run = runWith(client.MeshRunnerRunImplementation{GitlabCiCd: &client.MeshRunnerGitlabImplementation{PipelineTriggerToken: encrypted("trigger")}})
		decrypted, err = c.DecryptRun(run)
		require.NoError(t, err)
		assert.Equal(t, "trigger", decrypted.Spec.Definition.Spec.Implementation.GitlabCiCd.PipelineTriggerToken)

		run = runWith(client.MeshRunnerRunImplementation{AzureDevops: &client.MeshRunnerAzureDevopsImplementation{PersonalAccessToken: encrypted("pat")}})
		decrypted, err = c.DecryptRun(run)
		require.NoError(t, err)
		assert.Equal(t, "pat", decrypted.Spec.Definition.Spec.Implementation.AzureDevops.PersonalAccessToken)
	})

	t.Run("DecryptRun names the input it cannot decrypt", func(t *testing.T) {
		run := runWith(manual, client.MeshRunnerRunInput{Key: "secret", Value: encryptLikeMeshStack(t, generateKey(t), "plain"), IsSensitive: true})

		_, err := c.DecryptRun(run)

		assert.ErrorContains(t, err, "sensitive input secret: no private key decrypts the value")
	})
}
