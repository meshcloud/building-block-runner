package tfrun

// CP11 (PLAN_DETAIL_01_tf_characterization_tests.md §9): GitSource against local fixture
// repositories, black-box through the Worker where possible. Cloning + ref checkout run through the
// real Git facade (dtos.go wires gitFacade: &Git{}), so a refName resolves against the local repo's
// branches/tags/commits exactly as it would a remote (git.go:checkoutRef order). The missing-path
// and worktree-diagnostics branches round out gitsource.go's coverage.

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func (suite *WorkerTestSuite) Test_GitSource_MissingRepositoryPathFailsRun() {
	// The fixture repo exists, but the configured repository subpath does not — CopyToTargetDir
	// clones successfully then fails the os.Stat on the source subdir (gitsource.go:111-116).
	suite.calls.fetch = runDetailsFetchCall(withRepo(suite.repo.Path, "does/not/exist/in/repo"))
	updateCalls := suite.collectUpdatesWorker()

	suite.runWorker()

	final := decodeUpdate(suite.T(), (*updateCalls)[len(*updateCalls)-1])
	suite.Equal(FAILED.str(), *final.Status)
	sources := findStep(suite.T(), final, StepSources)
	suite.Equal(FAILED.str(), *sources.Status)
	suite.Require().NotNil(sources.SystemMessage)
	suite.Contains(*sources.SystemMessage, "does not exist")
}

func (suite *WorkerTestSuite) Test_GitSource_ChecksOutBranch() {
	repo := makeLocalGitRepo(suite.T(),
		map[string]string{"main.tf": "# root on master\n"},
		withGitBranch("feature", map[string]string{"main.tf": "# root on feature\n"}),
	)
	suite.calls.fetch = runDetailsFetchCall(withRepo(repo.Path, ""), withRefName("feature"))
	updateCalls := suite.collectUpdatesWorker()

	suite.runWorker()

	final := decodeUpdate(suite.T(), (*updateCalls)[len(*updateCalls)-1])
	suite.Equal(SUCCEEDED.str(), *final.Status, "checking out an existing branch must succeed")
}

func (suite *WorkerTestSuite) Test_GitSource_ChecksOutTag() {
	repo := makeLocalGitRepo(suite.T(),
		map[string]string{"main.tf": "# root\n"},
		withGitTag("v1.2.3"),
	)
	suite.calls.fetch = runDetailsFetchCall(withRepo(repo.Path, ""), withRefName("v1.2.3"))
	updateCalls := suite.collectUpdatesWorker()

	suite.runWorker()

	final := decodeUpdate(suite.T(), (*updateCalls)[len(*updateCalls)-1])
	suite.Equal(SUCCEEDED.str(), *final.Status, "checking out an existing tag must succeed")
}

func (suite *WorkerTestSuite) Test_GitSource_ChecksOutCommitHash() {
	repo := makeLocalGitRepo(suite.T(), map[string]string{"main.tf": "# root\n"})
	suite.calls.fetch = runDetailsFetchCall(withRepo(repo.Path, ""), withRefName(repo.Head.String()))
	updateCalls := suite.collectUpdatesWorker()

	suite.runWorker()

	final := decodeUpdate(suite.T(), (*updateCalls)[len(*updateCalls)-1])
	suite.Equal(SUCCEEDED.str(), *final.Status, "checking out a raw commit hash must succeed")
}

// Test_LogDirectoryContentsForWorktreeUnstagedChangedError drives the diagnostics helper directly
// with a directory holding a file, a subdirectory and a symlink, covering all three entry-type
// branches (gitsource.go:140-146). This is the one gitsource.go path not reachable via the Worker
// without a contrived facade error (§9 CP11).
func Test_LogDirectoryContentsForWorktreeUnstagedChangedError(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a-file.tf"), []byte("x"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "a-dir"), 0o700))
	require.NoError(t, os.Symlink(filepath.Join(dir, "a-file.tf"), filepath.Join(dir, "a-link")))

	gs := &GitSource{log: NewLogWrap(log.New(io.Discard, "", 0), filepath.Join(dir, "update.log"))}
	require.NotNil(t, gs.log)

	// Must not panic and must walk every entry type.
	assert.NotPanics(t, func() { gs.logDirectoryContentsForWorktreeUnstagedChangedError(dir) })
}
