//go:build !k8s

package config

import (
	"errors"

	"github.com/meshcloud/building-block-runner/internal/config/internal/legacy"
)

var errNoK8sSupport = errors.New("k8s needs a binary built with -tags k8s")

type K8s struct{}

func (*K8s) UnmarshalYAML([]byte) error {
	return errNoK8sSupport
}

func k8sFromLegacy(legacy.RunController) (*K8s, error) {
	return nil, nil
}

func (Config) ValidateK8s() error {
	return errNoK8sSupport
}
