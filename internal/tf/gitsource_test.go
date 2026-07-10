package tf

import (
	"io"
	"log/slog"
	"os"
	"path"
	"testing"

	"github.com/stretchr/testify/suite"
)

type GitSourceTestSuite struct {
	suite.Suite
	log     *logwrap
	logfile *os.File
	wd      string
}

func Test_GitSourceTestSuite(t *testing.T) {
	suite.Run(t, new(GitSourceTestSuite))
}

func (suite *GitSourceTestSuite) SetupSuite() {
	AppConfig = TfRunnerConfig{
		SkipHostKeyValidation: false,
	}

	tmpWd, err := os.MkdirTemp(os.TempDir(), "gitSourceTest-wd-")
	if err != nil {
		panic(err)
	}

	f, err := os.CreateTemp(os.TempDir(), "logfile")
	if err != nil {
		panic(err)
	}

	suite.wd = tmpWd
	suite.logfile = f
	lw, err := NewLogWrap(slog.New(slog.NewTextHandler(io.Discard, nil)), f.Name())
	if err != nil {
		panic(err)
	}
	suite.log = lw
}

func (suite *GitSourceTestSuite) TearDownSuite() {
	_ = suite.logfile.Close()
	_ = os.Remove(suite.logfile.Name())
	_ = os.RemoveAll(suite.wd)
}

func (suite *GitSourceTestSuite) Test_GitSourceCloneSimple() {

	mock := suite.newGitFacadeMock()
	auth := &NoAuth{}

	uut := GitSource{
		url:       "url",
		auth:      auth,
		gitFacade: mock,
		log:       suite.log,
	}

	err := uut.CopyToTargetDir(suite.wd)
	suite.Require().NoError(err)
	suite.True(mock.called("clone", auth, "url", path.Join(suite.wd, TEMP_CLONE_DIR_PATH)))
	suite.True(mock.neverCalled("checkoutRef"))
	suite.True(mock.neverCalled("azureClone"))
	suite.True(mock.neverCalled("azureCheckoutRef"))
}

func (suite *GitSourceTestSuite) Test_GitSourceCloneWithRef() {

	mock := suite.newGitFacadeMock()
	auth := &NoAuth{}

	uut := GitSource{
		url:       "url",
		refName:   p("ref"),
		auth:      auth,
		gitFacade: mock,
		log:       suite.log,
	}

	err := uut.CopyToTargetDir(suite.wd)
	suite.Require().NoError(err)
	suite.True(mock.called("clone", auth, "url", path.Join(suite.wd, TEMP_CLONE_DIR_PATH)))
	suite.True(mock.called("checkoutRef", Any{}, "ref"))
	suite.True(mock.neverCalled("azureClone"))
	suite.True(mock.neverCalled("azureCheckoutRef"))
}

func (suite *GitSourceTestSuite) Test_GitSourceCloneSimpleAzure() {

	mock := suite.newGitFacadeMock()
	auth := &NoAuth{}
	azureUrl := AZURE_DEVOPS_DOMAIN + "/url"

	uut := GitSource{
		url:       azureUrl,
		auth:      auth,
		gitFacade: mock,
		log:       suite.log,
	}

	err := uut.CopyToTargetDir(suite.wd)
	suite.Require().NoError(err)
	suite.True(mock.neverCalled("clone"))
	suite.True(mock.neverCalled("checkoutRef"))
	suite.True(mock.called("azureClone", suite.wd, azureUrl, nil, auth.name()))
	suite.True(mock.neverCalled("azureCheckoutRef"))
}

func (suite *GitSourceTestSuite) Test_GitSourceCloneWithRefAzure() {

	mock := suite.newGitFacadeMock()
	auth := &NoAuth{}
	azureUrl := AZURE_DEVOPS_DOMAIN + "/url"
	ref := p("ref")

	uut := GitSource{
		url:       azureUrl,
		refName:   ref,
		auth:      auth,
		gitFacade: mock,
		log:       suite.log,
	}

	err := uut.CopyToTargetDir(suite.wd)
	suite.Require().NoError(err)
	suite.True(mock.neverCalled("clone"))
	suite.True(mock.neverCalled("checkoutRef"))
	suite.True(mock.called("azureClone", suite.wd, azureUrl, ref, auth.name()))
	suite.True(mock.called("azureCheckoutRef", Any{}, *ref))
}

func (suite *GitSourceTestSuite) newGitFacadeMock() *MockedGitFacade {
	g := &MockedGitFacade{
		GitFacade: &Git{log: suite.log},
	}
	g.init()
	return g
}
