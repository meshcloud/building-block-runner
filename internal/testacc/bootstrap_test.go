package testacc

import (
	"context"
	"crypto/rand"
	"encoding/json/v2"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/executor/tf"
)

const (
	runnerResource        = "meshstack_building_block_runner.this"
	buildingBlockResource = "meshstack_building_block.this"

	// destroyTimeout outlasts the 5m delete timeout of a building block.
	destroyTimeout = 7 * time.Minute
)

// bootstrap applies testdata/bootstrap and testdata/<case>/bootstrap as one root module.
type bootstrap struct {
	tf tf.TfFacade
}

func (s *accSuite) newBootstrap(t *testing.T, testCase string, vars map[string]any) *bootstrap {
	t.Helper()
	dir := s.workDir(t, "bootstrap")
	require.NoError(t, os.CopyFS(dir, os.DirFS("testdata/bootstrap")))
	if caseDir := filepath.Join("testdata", testCase, "bootstrap"); exists(caseDir) {
		require.NoError(t, os.CopyFS(dir, os.DirFS(caseDir)))
	}
	encodedVars, err := json.Marshal(vars)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "harness.auto.tfvars.json"), encodedVars, 0o600))

	tofu, err := s.tofu.GetTF(t.Context(), dir, s.tofuVersion)
	require.NoError(t, err)
	require.NoError(t, tofu.SetEnv(s.tofuEnv(t)))
	require.NoError(t, tofu.Init(t.Context()))
	return &bootstrap{tf: tofu}
}

func (s *accSuite) tofuEnv(t *testing.T) map[string]string {
	t.Helper()
	env := map[string]string{
		"PATH":                         os.Getenv("PATH"),
		"HOME":                         s.workDir(t, "tofu-home"),
		"MESHSTACK_ENDPOINT":           s.endpoint,
		"MESHSTACK_API_KEY":            os.Getenv("MESHSTACK_API_KEY"),
		"MESHSTACK_API_SECRET":         os.Getenv("MESHSTACK_API_SECRET"),
		"MESHSTACK_SKIP_VERSION_CHECK": "1",
	}
	if s.tofuCliConfig != "" {
		env["TF_CLI_CONFIG_FILE"] = s.tofuCliConfig
	}
	return env
}

func (b *bootstrap) apply(t *testing.T, targets ...string) {
	t.Helper()
	options := make([]tfexec.ApplyOption, 0, len(targets))
	for _, target := range targets {
		options = append(options, tfexec.Target(target))
	}
	require.NoError(t, b.tf.Apply(t.Context(), options...))
}

func (b *bootstrap) destroy(t *testing.T, targets ...string) {
	t.Helper()
	options := make([]tfexec.DestroyOption, 0, len(targets))
	for _, target := range targets {
		options = append(options, tfexec.Target(target))
	}
	// A destroy runs as a cleanup, after t.Context() is cancelled.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), destroyTimeout)
	defer cancel()
	assert.NoError(t, b.tf.Destroy(ctx, options...))
}

func (b *bootstrap) output(t *testing.T, name string) string {
	t.Helper()
	outputs, err := b.tf.Output(t.Context())
	require.NoError(t, err)
	require.Contains(t, outputs, name)
	var value string
	require.NoError(t, json.Unmarshal(outputs[name].Value, &value))
	return value
}

// commitModule commits testdata/<case>/buildingblock to a repository of its own, which the runner clones by path.
func (s *accSuite) commitModule(t *testing.T, testCase string) string {
	t.Helper()
	dir := s.workDir(t, "repository")
	require.NoError(t, os.CopyFS(dir, os.DirFS(filepath.Join("testdata", testCase, "buildingblock"))))
	runGit(t, dir, "init", "--initial-branch=main")
	commitAll(t, dir, "the "+testCase+" building block")
	return "file://" + dir
}

func commitAll(t *testing.T, dir, message string) string {
	t.Helper()
	runGit(t, dir, "add", ".")
	runGit(t, dir, "-c", "commit.gpgsign=false", "commit", "--message", message)
	return runGit(t, dir, "rev-parse", "HEAD")
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	git := exec.CommandContext(t.Context(), "git", args...)
	git.Dir = dir
	git.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=testacc", "GIT_AUTHOR_EMAIL=testacc@example.com",
		"GIT_COMMITTER_NAME=testacc", "GIT_COMMITTER_EMAIL=testacc@example.com")
	output, err := git.CombinedOutput()
	require.NoErrorf(t, err, "git %s: %s", strings.Join(args, " "), output)
	return strings.TrimSpace(string(output))
}

// workDir outlives the test with RUNNER_TESTACC_KEEP=1, for a look at what a run left behind.
func (s *accSuite) workDir(t *testing.T, name string) string {
	t.Helper()
	if !s.keep {
		dir := filepath.Join(t.TempDir(), name)
		require.NoError(t, os.Mkdir(dir, 0o700))
		return dir
	}
	dir, err := os.MkdirTemp("", "runner-testacc-"+name+"-") //nolint:usetesting // a kept dir outlives the test.
	require.NoError(t, err)
	t.Logf("keeping %s", dir)
	return dir
}

// uniqueName is a valid workspace identifier.
func uniqueName(prefix string) string {
	return fmt.Sprintf("%s-%s", prefix, strings.ToLower(rand.Text()[:10]))
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
