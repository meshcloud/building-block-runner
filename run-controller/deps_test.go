//go:build !k8s

package main

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_BuildWithoutK8sTag_LinksNoKubernetesModule(t *testing.T) {
	out, err := exec.CommandContext(t.Context(), "go", "list", "-deps", ".").Output()
	require.NoError(t, err)

	for pkg := range strings.Lines(string(out)) {
		assert.NotContains(t, pkg, "k8s.io/")
	}
}
