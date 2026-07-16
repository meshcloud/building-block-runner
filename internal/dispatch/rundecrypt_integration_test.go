package dispatch_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	meshcrypto "github.com/meshcloud/building-block-runner/internal/crypto"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/rundecrypt"
	"github.com/meshcloud/building-block-runner/internal/secret"
)

// This is the one integrative test for the decrypt -> dispatch seam: a rundecrypt-wrapped
// handler running through the real InProcess dispatcher must see plaintext Details, using the
// same real CertDecryptor/keypair fixture internal/meshapi's own round-trip tests use.

type fakeHandler struct {
	observed chan *meshapi.Run
}

func (f *fakeHandler) Execute(_ context.Context, run dispatch.ClaimedRun) error {
	f.observed <- run.Run
	return nil
}

func testCertDecryptor(t *testing.T) (secret.Decryptor, *meshcrypto.MeshCertBasedCrypto) {
	t.Helper()
	pubKey, err := os.ReadFile("../resources/test.pem")
	require.NoError(t, err)
	privKeyPEM, err := os.ReadFile("../resources/test.key")
	require.NoError(t, err)

	full, err1, err2 := meshcrypto.NewCertBasedCrypto("../resources/test.key", pubKey)
	require.NoError(t, err1)
	require.NoError(t, err2)

	dec, err := secret.NewCertDecryptor(string(privKeyPEM))
	require.NoError(t, err)
	return dec, full
}

func TestRunDecrypt_ThroughInProcessDispatch_HandlerObservesPlaintext(t *testing.T) {
	dec, full := testCertDecryptor(t)

	ciphertext, err := full.EncryptMeshCertBased("input-secret")
	require.NoError(t, err)

	dto := meshapi.Run{
		Metadata: meshapi.RunMetaDTO{Uuid: "run-uuid"},
		Spec: meshapi.RunSpecDTO{
			BuildingBlock: meshapi.BuildingBlockSpecDTO{
				Spec: meshapi.BuildingBlockDetailsSpecDTO{
					Inputs: []meshapi.BuildingBlockInputSpecDTO{
						{Key: "password", Value: ciphertext, Type: secret.TypeString, IsSensitive: true},
					},
				},
			},
			Definition: meshapi.DefinitionSpecDTO{
				Spec: meshapi.DefinitionDetailsSpecDTO{
					Implementation: json.RawMessage(`{"type":"MANUAL"}`),
				},
			},
		},
	}
	raw, err := json.Marshal(dto)
	require.NoError(t, err)
	rawJson := base64.StdEncoding.EncodeToString(raw)

	handler := &fakeHandler{observed: make(chan *meshapi.Run, 1)}
	wrapped := rundecrypt.Wrap(handler, dec)

	d, err := dispatch.NewInProcess(map[meshapi.RunnerImplementationType]dispatch.RunHandler{
		meshapi.RunnerTypeManual: wrapped,
	}, 0, nil)
	require.NoError(t, err)

	err = d.Dispatch(dispatch.ClaimedRun{
		Id:      "run-1",
		Type:    meshapi.RunnerTypeManual,
		RawJson: rawJson,
	})
	require.NoError(t, err)

	select {
	case got := <-handler.observed:
		require.Equal(t, "input-secret", got.Spec.BuildingBlock.Spec.Inputs[0].Value)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for handler to observe dispatched run")
	}

	d.Wait()
}
