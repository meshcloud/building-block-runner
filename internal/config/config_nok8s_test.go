//go:build !k8s

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_LoadWithoutK8sSupport_RegistersForAllAndRejectsAK8sSection(t *testing.T) {
	ensureCleanRunnerEnv(t)

	t.Run("legacy k8s layout registers for ALL", func(t *testing.T) {
		cfg := loadTestdataFile(t, "runner-config-legacy-k8s.yml")
		assert.Nil(t, cfg.K8s)
		assert.Equal(t, RunnerTypes{RunnerTypeAll}, cfg.Dispatcher.ForTypes)
	})

	t.Run("a k8s section fails", func(t *testing.T) {
		_, err := Load(t.Context(), []byte("k8s:\n  namespace: default\n"))
		require.ErrorContains(t, err, "k8s needs a binary built with -tags k8s")
		assert.ErrorIs(t, new(Config).ValidateK8s(), errNoK8sSupport)
	})
}
