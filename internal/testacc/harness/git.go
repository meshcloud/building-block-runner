package harness

import (
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func (s Suite) CommitModule(t *testing.T, module fs.FS) string {
	t.Helper()
	return "file://" + s.CommitRepository(t, module)
}

func (s Suite) CommitRepository(t *testing.T, files fs.FS) string {
	t.Helper()
	dir := s.WorkDir(t, "repository")
	require.NoError(t, os.CopyFS(dir, files))
	RunGit(t, dir, "init", "--initial-branch=main")
	CommitAll(t, dir, "the module")
	return dir
}

func CommitAll(t *testing.T, dir, message string) string {
	t.Helper()
	RunGit(t, dir, "add", ".")
	RunGit(t, dir, "-c", "commit.gpgsign=false", "commit", "--message", message)
	return RunGit(t, dir, "rev-parse", "HEAD")
}

func RunGit(t *testing.T, dir string, args ...string) string {
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
