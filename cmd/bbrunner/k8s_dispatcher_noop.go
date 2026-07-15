//go:build !k8s

package main

import (
	"errors"
	"log/slog"

	"github.com/meshcloud/building-block-runner/internal/config"
	meshcrypto "github.com/meshcloud/building-block-runner/internal/crypto"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
)

// This file is the no-op fallback half of the k8s dispatcher factory seam (paired with the real
// implementation in k8s_dispatcher.go, `k8s`): the default no-tag build — the in-process
// superset — links this file instead of the real one, so internal/k8sjob's client-go-backed Job
// dispatcher construction (and client-go itself) never gets linked into the superset binary.
// It exists so runController's dispatcher-kind branch never needs its own build tags: both
// halves expose the identical two functions.

// errK8sNotBuilt is returned by newK8sJobDispatcher when this binary was built without the k8s
// tag. This is a build-time choice (the lean, Kubernetes-free image), not a runtime
// auto-detect miss, so it fails loudly rather than silently falling back to some other
// behavior.
var errK8sNotBuilt = errors.New("bbrunner: this build was compiled without Kubernetes Job dispatch support (missing the k8s build tag); run the in-process superset instead (RUNNER_DISPATCHER=inprocess), or use a build that links the k8s tag")

// newK8sJobDispatcher always fails: see errK8sNotBuilt.
func newK8sJobDispatcher(cfg *config.ControllerConfig, cryptoInstance *meshcrypto.MeshCertBasedCrypto, metrics *dispatch.MetricsCollector, logger *slog.Logger) (dispatch.Dispatcher, error) {
	return nil, errK8sNotBuilt
}

// discoverOIDCIssuer always returns "" (no issuer), matching the real implementation's failure
// return (an undiscoverable issuer never fails the caller -- see k8sjob.DiscoverOIDCIssuer).
func discoverOIDCIssuer(logger *slog.Logger) string {
	return ""
}
