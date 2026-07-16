//go:build k8s || !lean

package k8sjob

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/meshcloud/building-block-runner/internal/config"
	meshcrypto "github.com/meshcloud/building-block-runner/internal/crypto"
)

// NewKubernetesJobDispatcher creates a dispatcher backed by a real cluster connection
// (in-cluster config, or the standard client-go kubeconfig precedence out of cluster). It
// lives in this file (not kubernetes.go) specifically so its real-I/O cluster-connection
// step stays covered by the same per-file coverage exclusion as getKubernetesConfig/
// DiscoverOIDCIssuer; NewKubernetesJobDispatcherWithClient (kubernetes.go) is the
// hermetically-testable half every test in this package actually exercises.
func NewKubernetesJobDispatcher(cfg config.K8sJobConfig, runnerUuid, apiUrl string, crypto *meshcrypto.MeshCertBasedCrypto, metrics JobMetrics, logger *slog.Logger) (*KubernetesJobDispatcher, error) {
	restConfig, err := getKubernetesConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get kubernetes config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	return NewKubernetesJobDispatcherWithClient(clientset, cfg, runnerUuid, apiUrl, crypto, metrics, logger), nil
}

// getKubernetesConfig and DiscoverOIDCIssuer are the package's only real-cluster I/O (live
// kubeconfig loading, HTTP against the API server's OIDC discovery endpoint) -- isolated in
// this one file precisely so the coverage exclusion list (tools/coverage/exclusions.txt)
// stays per-file honest. Everything else in this package
// is hermetically testable via k8s.io/client-go/kubernetes/fake.

func getKubernetesConfig() (*rest.Config, error) {
	// This uses the standard Kubernetes client-go precedence:
	// 1. In-cluster config (when running inside a pod)
	// 2. KUBECONFIG environment variable
	// 3. $HOME/.kube/config
	// This is the idiomatic way and handles all edge cases properly
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
	return kubeConfig.ClientConfig()
}

// DiscoverOIDCIssuer attempts to discover the OIDC issuer URL from the Kubernetes API server.
// It queries the /.well-known/openid-configuration endpoint on the API server.
// Returns empty string if discovery fails (e.g., not running in cluster or OIDC not configured).
func DiscoverOIDCIssuer(logger *slog.Logger) string {
	config, err := getKubernetesConfig()
	if err != nil {
		logger.Warn("failed to get Kubernetes config for OIDC discovery", "error", err)
		return ""
	}

	// Build the OIDC configuration URL from the API server host
	// The Kubernetes API server exposes /.well-known/openid-configuration
	apiServerURL := strings.TrimSuffix(config.Host, "/")
	oidcConfigURL := apiServerURL + "/.well-known/openid-configuration"

	logger.Info("discovering OIDC issuer", "url", oidcConfigURL)

	// Create HTTP client with the same TLS config as the Kubernetes client
	transport, err := rest.TransportFor(config)
	if err != nil {
		logger.Warn("failed to create transport for OIDC discovery", "error", err)
		return ""
	}

	client := &http.Client{Transport: transport}

	resp, err := client.Get(oidcConfigURL)
	if err != nil {
		logger.Warn("failed to fetch OIDC configuration", "error", err)
		return ""
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		logger.Warn("OIDC configuration endpoint returned non-OK status", "statusCode", resp.StatusCode)
		return ""
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Warn("failed to read OIDC configuration response", "error", err)
		return ""
	}

	// Parse the OpenID configuration to extract the issuer
	var oidcConfig struct {
		Issuer string `json:"issuer"`
	}

	if err := json.Unmarshal(body, &oidcConfig); err != nil {
		logger.Warn("failed to parse OIDC configuration", "error", err)
		return ""
	}

	if oidcConfig.Issuer == "" {
		logger.Warn("OIDC configuration does not contain an issuer")
		return ""
	}

	logger.Info("discovered OIDC issuer", "issuer", oidcConfig.Issuer)
	return oidcConfig.Issuer
}
