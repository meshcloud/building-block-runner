package crypto

import (
	"encoding/json/jsontext"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/meshstack-cli/client"
)

func runWith(implementation string, inputs ...client.MeshRunnerRunInput) client.MeshRunnerRun {
	var run client.MeshRunnerRun
	run.Spec.RunToken = "a-run-token"
	run.Spec.BuildingBlock.Spec.Inputs = inputs
	run.Spec.Definition.Spec.Implementation = jsontext.Value(implementation)
	return run
}

func TestDecryptRun(t *testing.T) {
	key := generateKey(t)
	c := Crypto{PrivateKeys: PrivateKeys{{key}}}
	encrypted := func(plainText string) string {
		return `"` + encryptLikeMeshStack(t, key, plainText) + `"`
	}

	t.Run("decrypts sensitive string inputs only", func(t *testing.T) {
		run := runWith(`{"type":"MANUAL"}`,
			client.MeshRunnerRunInput{Key: "secret", Value: jsontext.Value(encrypted("plain")), IsSensitive: true},
			client.MeshRunnerRunInput{Key: "number", Value: jsontext.Value(`42`), IsSensitive: true},
			client.MeshRunnerRunInput{Key: "empty", Value: jsontext.Value(`""`), IsSensitive: true},
			client.MeshRunnerRunInput{Key: "public", Value: jsontext.Value(`"as is"`)},
		)

		decrypted, err := c.DecryptRun(run)

		require.NoError(t, err)
		var values []string
		for _, input := range decrypted.Spec.BuildingBlock.Spec.Inputs {
			values = append(values, input.Value.String())
		}
		assert.Equal(t, []string{`"plain"`, `42`, `""`, `"as is"`}, values)
		assert.NotEqual(t, run.Spec.BuildingBlock.Spec.Inputs[0].Value, decrypted.Spec.BuildingBlock.Spec.Inputs[0].Value)
	})

	for implementationType, secretKey := range implementationSecretKeys {
		t.Run("decrypts "+secretKey+" of "+string(implementationType), func(t *testing.T) {
			run := runWith(`{"type":"` + string(implementationType) + `","` + secretKey + `":` + encrypted("plain") + `,"unknown":1}`)

			decrypted, err := c.DecryptRun(run)

			require.NoError(t, err)
			assert.JSONEq(t, `{"type":"`+string(implementationType)+`","`+secretKey+`":"plain","unknown":1}`, decrypted.Spec.Definition.Spec.Implementation.String())
		})
	}

	t.Run("fails on a value for another key", func(t *testing.T) {
		run := runWith(`{"type":"MANUAL"}`, client.MeshRunnerRunInput{Key: "secret", Value: jsontext.Value(`"` + encryptLikeMeshStack(t, generateKey(t), "plain") + `"`), IsSensitive: true})

		_, err := c.DecryptRun(run)

		assert.ErrorContains(t, err, "sensitive input secret: no private key decrypts the value")
	})
}

func TestRedactRun(t *testing.T) {
	run := runWith(`{"type":"TERRAFORM","sshPrivateKey":"key","repositoryUrl":"git@example.com"}`,
		client.MeshRunnerRunInput{Key: "secret", Value: jsontext.Value(`"plain"`), IsSensitive: true},
		client.MeshRunnerRunInput{Key: "public", Value: jsontext.Value(`"as is"`)},
	)

	redacted, err := RedactRun(run)

	require.NoError(t, err)
	assert.Equal(t, "<redacted>", redacted.Spec.RunToken)
	assert.Equal(t, `"<redacted>"`, redacted.Spec.BuildingBlock.Spec.Inputs[0].Value.String())
	assert.Equal(t, `"as is"`, redacted.Spec.BuildingBlock.Spec.Inputs[1].Value.String())
	assert.JSONEq(t, `{"type":"TERRAFORM","sshPrivateKey":"<redacted>","repositoryUrl":"git@example.com"}`, redacted.Spec.Definition.Spec.Implementation.String())
	assert.Equal(t, "a-run-token", run.Spec.RunToken)
}
