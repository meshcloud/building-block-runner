//go:build !k8s

package config

import (
	"io"
	"log"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoadWithoutK8sSupport(t *testing.T) {
	t.Run("runner-config-old-k8s.yml registers the runner types for the in-process dispatcher", func(t *testing.T) {
		unsetRunnerEnv(t)
		cfg := load(t, "runner-config-old-k8s.yml")
		assert.Nil(t, cfg.K8s)
		assert.Equal(t, RunnerTypes{RunnerTypeTf, RunnerTypeManual, RunnerTypeGitHub, RunnerTypeGitLab, RunnerTypeAzureDevOps}, cfg.Dispatcher.ForTypes)
	})

	t.Run("a k8s section fails", func(t *testing.T) {
		unsetRunnerEnv(t)
		_, err := Load([]byte("k8s:\n  namespace: default\n"), log.New(io.Discard, "", 0))
		assert.ErrorContains(t, err, "k8s needs a binary built with -tags k8s")
	})

	t.Run("ValidateK8s fails", func(t *testing.T) {
		assert.ErrorIs(t, Config{}.ValidateK8s(), errNoK8sSupport)
	})
}
