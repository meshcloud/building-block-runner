package tf

// GitSource against local fixture
// repositories, black-box through the Worker where possible. Cloning + ref checkout run through the
// real Git facade (dtos.go wires gitFacade: &Git{}), so a refName resolves against the local repo's
// branches/tags/commits exactly as it would a remote (git.go:checkoutRef order). The missing-path
// and worktree-diagnostics branches round out gitsource.go's coverage.

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/report"
)

func (suite *WorkerTestSuite) Test_GitSource_MissingRepositoryPathFailsRun() {
	// The fixture repo exists, but the configured repository subpath does not — CopyToTargetDir
	// clones successfully then fails the os.Stat on the source subdir (gitsource.go:111-116).
	suite.calls.fetch = runDetailsFetchCall(withRepo(suite.repo.Path, "does/not/exist/in/repo"))
	updateCalls := suite.collectUpdatesWorker()

	suite.runWorker()

	final := decodeUpdate(suite.T(), (*updateCalls)[len(*updateCalls)-1])
	suite.Equal(report.FAILED.String(), *final.Status)
	sources := findStep(suite.T(), final, StepSources)
	suite.Equal(report.FAILED.String(), *sources.Status)
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
	suite.Equal(report.SUCCEEDED.String(), *final.Status, "checking out an existing branch must succeed")
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
	suite.Equal(report.SUCCEEDED.String(), *final.Status, "checking out an existing tag must succeed")
}

func (suite *WorkerTestSuite) Test_GitSource_ChecksOutCommitHash() {
	repo := makeLocalGitRepo(suite.T(), map[string]string{"main.tf": "# root\n"})
	suite.calls.fetch = runDetailsFetchCall(withRepo(repo.Path, ""), withRefName(repo.Head.String()))
	updateCalls := suite.collectUpdatesWorker()

	suite.runWorker()

	final := decodeUpdate(suite.T(), (*updateCalls)[len(*updateCalls)-1])
	suite.Equal(report.SUCCEEDED.String(), *final.Status, "checking out a raw commit hash must succeed")
}

// Test_LogDirectoryContentsForWorktreeUnstagedChangedError drives the diagnostics helper directly
// with a directory holding a file, a subdirectory and a symlink, covering all three entry-type
// branches (gitsource.go:140-146). This is the one gitsource.go path not reachable via the Worker
// without a contrived facade error.
func Test_LogDirectoryContentsForWorktreeUnstagedChangedError(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a-file.tf"), []byte("x"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "a-dir"), 0o700))
	require.NoError(t, os.Symlink(filepath.Join(dir, "a-file.tf"), filepath.Join(dir, "a-link")))

	lw, err := NewLogWrap(slog.New(slog.NewTextHandler(io.Discard, nil)), filepath.Join(dir, "update.log"))
	require.NoError(t, err)
	gs := &GitSource{log: lw}

	// Must not panic and must walk every entry type.
	assert.NotPanics(t, func() { gs.logDirectoryContentsForWorktreeUnstagedChangedError(dir) })
}

// vanishingTmpDirFacade wraps MockedGitFacade's no-op clone() to also remove the tmp clone dir it
// was asked to clone into, simulating "the cloned tmp directory vanished between clone and the
// subsequent os.Stat" — a race unreachable via the real Git facade in a hermetic test.
type vanishingTmpDirFacade struct {
	*MockedGitFacade
}

func (g *vanishingTmpDirFacade) clone(a auth, url, targetdir string) (*git.Repository, error) {
	repo, err := g.MockedGitFacade.clone(a, url, targetdir)
	if err == nil {
		err = os.RemoveAll(targetdir)
	}
	return repo, err
}

// Test_CopyToTargetDir_NilPathMissingSourceDirDoesNotPanic pins the fixed gitsource.go:111-116
// when the resolved sourceDir unexpectedly does not exist and g.path is nil (no subpath
// configured), CopyToTargetDir must log the resolved sourceDir and return an error — not
// nil-deref on *g.path, which is nil in exactly this case.
func Test_CopyToTargetDir_NilPathMissingSourceDirDoesNotPanic(t *testing.T) {
	dir := t.TempDir()
	lw, err := NewLogWrap(slog.New(slog.NewTextHandler(io.Discard, nil)), filepath.Join(dir, "update.log"))
	require.NoError(t, err)

	facade := &vanishingTmpDirFacade{MockedGitFacade: &MockedGitFacade{}}
	facade.init()

	gs := &GitSource{
		url:       "https://example.com/org/repo.git",
		path:      nil,
		auth:      &NoAuth{},
		log:       lw,
		gitFacade: facade,
	}

	var copyErr error
	assert.NotPanics(t, func() { copyErr = gs.CopyToTargetDir(dir) })
	assert.ErrorContains(t, copyErr, "specified path does not exist")
}
