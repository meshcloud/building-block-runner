//go:build !k8s

package config

import (
	"io"
	"log"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_LoadWithoutK8sSupport_RegistersForAllAndRejectsAK8sSection(t *testing.T) {
	unsetRunnerEnv(t)

	t.Run("legacy k8s layout registers for ALL", func(t *testing.T) {
		cfg, _ := loadFile(t, "runner-config-old-k8s.yml")
		assert.Nil(t, cfg.K8s)
		assert.Equal(t, RunnerTypes{RunnerTypeAll}, cfg.Dispatcher.ForTypes)
	})

	t.Run("a k8s section fails", func(t *testing.T) {
		_, err := Load([]byte("k8s:\n  namespace: default\n"), log.New(io.Discard, "", 0))
		assert.ErrorContains(t, err, "k8s needs a binary built with -tags k8s")
		assert.ErrorIs(t, Config{}.ValidateK8s(), errNoK8sSupport)
	})
}
