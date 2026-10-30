package harness

import (
	"context"
	_ "embed"
	"encoding/json/v2"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/v2/internal/executor/tf"
)

const (
	runnerResource        = "meshstack_building_block_runner.this"
	buildingBlockResource = "meshstack_building_block.this"

	// destroyTimeout outlasts the 5m delete timeout of a building block.
	destroyTimeout = 7 * time.Minute
)

//go:embed runner.tf
var runnerTf []byte

type Bootstrap struct {
	tf tf.Facade
}

func newBootstrap(t *testing.T, s Suite, caseBootstrap fs.FS, vars map[string]any) Bootstrap {
	t.Helper()
	dir := s.WorkDir(t, "bootstrap")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "runner.tf"), runnerTf, 0o600))
	if caseBootstrap != nil {
		require.NoError(t, os.CopyFS(dir, caseBootstrap))
	}
	encodedVars, err := json.Marshal(vars)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "harness.auto.tfvars.json"), encodedVars, 0o600))

	tofu, err := s.Tofu.GetTF(t.Context(), dir, s.TofuVersion)
	require.NoError(t, err)
	require.NoError(t, tofu.SetEnv(tofuEnv(t, s)))
	require.NoError(t, tofu.Init(t.Context()))
	return Bootstrap{tf: tofu}
}

func tofuEnv(t *testing.T, s Suite) map[string]string {
	t.Helper()
	env := map[string]string{
		"PATH":                         os.Getenv("PATH"),
		"HOME":                         s.WorkDir(t, "tofu-home"),
		"MESHSTACK_ENDPOINT":           s.Endpoint,
		"MESHSTACK_API_KEY":            os.Getenv("MESHSTACK_API_KEY"),
		"MESHSTACK_API_SECRET":         os.Getenv("MESHSTACK_API_SECRET"),
		"MESHSTACK_SKIP_VERSION_CHECK": "1",
	}
	if s.TofuCliConfig != "" {
		env["TF_CLI_CONFIG_FILE"] = s.TofuCliConfig
	}
	return env
}

func (b Bootstrap) Apply(t *testing.T, targets ...string) {
	t.Helper()
	options := make([]tfexec.ApplyOption, 0, len(targets))
	for _, target := range targets {
		options = append(options, tfexec.Target(target))
	}
	require.NoError(t, b.tf.Apply(t.Context(), options...))
}

func (b Bootstrap) Destroy(t *testing.T, targets ...string) {
	t.Helper()
	options := make([]tfexec.DestroyOption, 0, len(targets))
	for _, target := range targets {
		options = append(options, tfexec.Target(target))
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), destroyTimeout)
	defer cancel()
	assert.NoError(t, b.tf.Destroy(ctx, options...))
}

func (b Bootstrap) Output[T any](t *testing.T, name string) T {
	t.Helper()
	outputs, err := b.tf.Output(t.Context())
	require.NoError(t, err)
	require.Contains(t, outputs, name)
	var value T
	require.NoError(t, json.Unmarshal(outputs[name].Value, &value))
	return value
}
