package main

// superset_test.go pins the controller in-process superset wiring (P2.1): the run-controller
// image, run out-of-cluster (or with RUNNER_DISPATCHER=inprocess), composes EVERY linked
// persona handler into one dispatcher instead of failing fast. These are wiring assertions
// (the handler set + dispatcher selection), not live-meshStack behavior (that is the P0.1
// acceptance gate); per-handler behavior is covered by the internal-package suites.

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// testPrivateKeyPEM generates a throwaway PKCS#1 RSA private key PEM the per-persona
// cert decryptors (meshapi/github/tf) all parse via internal/crypto.
func testPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der := x509.MarshalPKCS1PrivateKey(key)
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}))
}

// TestBuildSupersetHandlers_RegistersEveryType proves the superset composes a handler for all
// five concrete run types from one shared connection, and that the composed set is accepted by
// the InProcess dispatcher -- i.e. RUNNER_DISPATCHER=inprocess on the controller no longer
// fails fast, every claimed type now has an in-process handler (P2.1).
func TestBuildSupersetHandlers_RegistersEveryType(t *testing.T) {
	handlers, err := buildSupersetHandlers("http://localhost:8080", "controller-uuid", testPrivateKeyPEM(t), nil, discardLogger())
	require.NoError(t, err)

	want := []meshapi.RunnerImplementationType{
		meshapi.RunnerTypeManual,
		meshapi.RunnerTypeTerraform,
		meshapi.RunnerTypeGitHubWorkflow,
		meshapi.RunnerTypeGitLabPipeline,
		meshapi.RunnerTypeAzureDevOpsPipeline,
	}
	require.Len(t, handlers, len(want), "superset must register exactly the five concrete run types")
	for _, typ := range want {
		require.Contains(t, handlers, typ, "superset must register a handler for %q", typ)
		require.NotNil(t, handlers[typ], "handler for %q must not be nil", typ)
	}

	// The composed set builds a valid InProcess dispatcher (no ALL-type / nil-handler rejection):
	// the concrete replacement for the removed fail-fast branch.
	_, err = dispatch.NewInProcess(handlers, 0, discardLogger())
	require.NoError(t, err)
}

// TestBuildSupersetHandlers_RejectsBadKey guards that a broken controller private key surfaces
// as a build error (fail-fast, P5) rather than a silently key-less superset.
func TestBuildSupersetHandlers_RejectsBadKey(t *testing.T) {
	_, err := buildSupersetHandlers("http://localhost:8080", "controller-uuid", "not a pem", nil, discardLogger())
	require.Error(t, err)
}

// TestControllerDispatcherSelection pins the run-controller's dispatcher choice (P2.1): the
// in-cluster default (KUBERNETES_SERVICE_HOST present, no override) builds the Kubernetes-Job
// path, byte-identical to before; out-of-cluster, or an explicit RUNNER_DISPATCHER=inprocess,
// builds the all-types in-process superset. This is the routing decision runController makes
// (detectDispatcherKind); the fail-fast on inprocess is gone.
func TestControllerDispatcherSelection(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want dispatcherKind
	}{
		{"in-cluster default -> k8sjob (published behavior, unchanged)", map[string]string{"KUBERNETES_SERVICE_HOST": "10.0.0.1"}, dispatcherK8sJob},
		{"out-of-cluster -> in-process superset", map[string]string{}, dispatcherInProcess},
		{"explicit RUNNER_DISPATCHER=inprocess -> superset (no more fail-fast)", map[string]string{"RUNNER_DISPATCHER": "inprocess", "KUBERNETES_SERVICE_HOST": "10.0.0.1"}, dispatcherInProcess},
		{"explicit RUNNER_DISPATCHER=k8sjob out-of-cluster -> k8sjob", map[string]string{"RUNNER_DISPATCHER": "k8sjob"}, dispatcherK8sJob},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectDispatcherKind(func(k string) string { return tt.env[k] })
			require.Equal(t, tt.want, got)
		})
	}
}
