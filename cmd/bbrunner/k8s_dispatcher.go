//go:build k8s

package main

import (
	"log/slog"

	"github.com/meshcloud/building-block-runner/internal/config"
	meshcrypto "github.com/meshcloud/building-block-runner/internal/crypto"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/k8sjob"
)

// This file is the real half of the k8s dispatcher factory seam (paired with the no-op fallback
// in k8s_dispatcher_noop.go, `!k8s`): it is the only place in cmd/bbrunner that imports
// internal/k8sjob's client-go-backed Job dispatcher construction. It is linked ONLY by the
// `-tags k8s` build — the lean run-controller image, which dispatches k8s Jobs and links no
// in-process type handlers. The default no-tag build (the in-process superset) links the no-op
// half instead and never pulls client-go into the binary.

// newK8sJobDispatcher builds the real in-cluster Kubernetes-Job dispatcher.
func newK8sJobDispatcher(cfg *config.ControllerConfig, cryptoInstance *meshcrypto.MeshCertBasedCrypto, metrics *dispatch.MetricsCollector, logger *slog.Logger) (dispatch.Dispatcher, error) {
	return k8sjob.NewKubernetesJobDispatcher(cfg.K8sJobConfig, cfg.Uuid, cfg.Api.Url, cryptoInstance, metrics, logger)
}

// discoverOIDCIssuer discovers the in-cluster OIDC issuer for WIF from the Kubernetes API.
func discoverOIDCIssuer(logger *slog.Logger) string {
	return k8sjob.DiscoverOIDCIssuer(logger)
}
