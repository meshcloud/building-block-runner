package tfrun

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"

	"github.com/hashicorp/terraform-exec/tfexec"
	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
	"github.com/stretchr/testify/assert"
)

const (
	noopBlockRepo = "https://github.com/meshcloud/meshstack-hub.git"
	noopBlockPath = "modules/meshstack/noop/buildingblock"
)

// generatedBackendDuringInit returns the meshStack backend file as tofu init finds it. The run's working
// directory is removed once the run ends.
func (suite *WorkerTestSuite) generatedBackendDuringInit() *string {
	backend := new(string)
	suite.tfMock.initFunc = func(ctx context.Context, opts ...tfexec.InitOption) error {
		wd := ctx.Value(runInfoContextKey).(*RunContextInfo).workingDirectory
		matches, err := fs.Glob(os.DirFS(wd), "meshStack_httpbackend-*.tf")
		if err != nil {
			return err
		}
		if len(matches) != 1 {
			return fmt.Errorf("want one generated backend file, found %v", matches)
		}
		content, err := os.ReadFile(path.Join(wd, matches[0]))
		*backend = string(content)
		return err
	}
	return backend
}

func meshBackendImplementation() meshapi.TerraformImplementation {
	return meshapi.TerraformImplementation{
		TerraformVersion:           DEFAULT_TF_VER,
		RepositoryUrl:              noopBlockRepo,
		RepositoryPath:             new(noopBlockPath),
		UseMeshHttpBackendFallback: true,
	}
}

func (suite *WorkerTestSuite) Test_RunWithTfStateLockLink_GeneratesABackendThatLocks() {
	lockHref := "http://localhost/api/terraform/state/workspace/ws/buildingBlock/block-uuid/lock"
	suite.calls.fetch = mockRunDetailsFetchCall(APPLY.str(), meshBackendImplementation(), meshapi.LinksDTO{
		MeshstackBaseUrl: meshapi.LinkDTO{Href: "http://localhost"},
		TfStateLock:      meshapi.LinkDTO{Href: lockHref},
	})
	backend := suite.generatedBackendDuringInit()

	suite.runWorker()

	assertContainsHCL(suite.T(), *backend, `lock_address = "`+lockHref+`"`)
	assertContainsHCL(suite.T(), *backend, `lock_method = "POST"`)
	assertContainsHCL(suite.T(), *backend, `unlock_address = "`+lockHref+`"`)
	assertContainsHCL(suite.T(), *backend, `unlock_method = "DELETE"`)
}

func (suite *WorkerTestSuite) Test_RunWithoutTfStateLockLink_GeneratesABackendThatDoesNotLock() {
	suite.calls.fetch = mockRunDetailsFetchCall(APPLY.str(), meshBackendImplementation(), meshapi.LinksDTO{
		MeshstackBaseUrl: meshapi.LinkDTO{Href: "http://localhost"},
	})
	backend := suite.generatedBackendDuringInit()

	suite.runWorker()

	assert.Contains(suite.T(), *backend, "address")
	assert.NotContains(suite.T(), *backend, "lock_address")
	assert.NotContains(suite.T(), *backend, "lock_method")
}

func (suite *WorkerTestSuite) Test_Apply_WaitsForTheStateLock() {
	suite.calls.fetch = mockValidRunDetailsFetchCall(APPLY.str(), noopBlockRepo, noopBlockPath)
	var applyOpts []tfexec.ApplyOption
	suite.tfMock.applyFunc = func(ctx context.Context, opts ...tfexec.ApplyOption) error {
		applyOpts = opts
		return nil
	}

	suite.runWorker()

	assert.Contains(suite.T(), applyOpts, tfexec.LockTimeout("5m"))
}

func (suite *WorkerTestSuite) Test_Destroy_WaitsForTheStateLock() {
	suite.calls.fetch = mockValidRunDetailsFetchCall(DESTROY.str(), noopBlockRepo, noopBlockPath)
	var destroyOpts []tfexec.DestroyOption
	suite.tfMock.destroyFunc = func(ctx context.Context, opts ...tfexec.DestroyOption) error {
		destroyOpts = opts
		return nil
	}

	suite.runWorker()

	assert.Contains(suite.T(), destroyOpts, tfexec.LockTimeout("5m"))
}

func (suite *WorkerTestSuite) Test_Detect_WaitsForTheStateLock() {
	suite.calls.fetch = mockValidRunDetailsFetchCall(DETECT.str(), noopBlockRepo, noopBlockPath)
	var planOpts []tfexec.PlanOption
	suite.tfMock.planFunc = func(ctx context.Context, opts ...tfexec.PlanOption) (bool, error) {
		planOpts = opts
		return false, nil
	}

	suite.runWorker()

	assert.Contains(suite.T(), planOpts, tfexec.LockTimeout("5m"))
}
