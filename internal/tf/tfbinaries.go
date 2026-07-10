package tf

import (
	"context"
	_ "embed"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/hashicorp/go-version"
	"github.com/hashicorp/hc-install/product"
	"github.com/hashicorp/hc-install/releases"
	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/opentofu/tofudl"
)

//
// TFBinaries is a wrapper for downloading terraform executables and store them locally.
// We store each downloaded version in a separate folder and return an already downloaded
// version in case it is requested again.
// This way we can prevent multiple downloads of the same version on this RUNNER node.
//

//go:embed hashicorp-public-key.asc
var hashicorpSecurityArmoredPublicKey string

type TfBinaries struct {
	dir      string
	logger   *log.Logger
	mutex    sync.Mutex
	testMode bool
	tfMock   *MockedTfFacade
}

func NewTfBin(parentInstallDir string, writer io.Writer) (*TfBinaries, error) {
	installer := &TfBinaries{
		dir:      parentInstallDir,
		logger:   log.New(writer, "[TfBinaries] ", log.LstdFlags),
		mutex:    sync.Mutex{},
		testMode: false,
		tfMock:   nil,
	}

	err := installer.prepareInstallDir()

	if err != nil {
		return nil, err
	} else {
		return installer, nil
	}
}

func ForTestNewTfBin(parentInstallDir string, writer io.Writer, mock *MockedTfFacade) (*TfBinaries, error) {
	installer := &TfBinaries{
		dir:      parentInstallDir,
		logger:   log.New(writer, "[TfBinaries] ", log.LstdFlags),
		mutex:    sync.Mutex{},
		testMode: true,
		tfMock:   mock,
	}

	err := installer.prepareInstallDir()

	if err != nil {
		return nil, err
	} else {
		return installer, nil
	}
}

func (bin *TfBinaries) prepareInstallDir() error {
	err := os.MkdirAll(bin.dir, 0777)
	return err
}

func (bin *TfBinaries) GetTF(ctx context.Context, workingDir string, ver string) (TfFacade, error) {

	// in testMode provide a mocked TfFacade, either the specified one or a default mock
	if bin.testMode {
		if bin.tfMock != nil {
			bin.tfMock.workingDir = workingDir
			return bin.tfMock, nil
		} else {
			mock := &MockedTfFacade{}
			mock.initMockFuncs()
			mock.workingDir = workingDir
			return mock, nil
		}
	}

	bin.mutex.Lock()
	defer bin.mutex.Unlock()

	expectedExecPath := filepath.Join(bin.dir, ver, product.Terraform.BinaryName())
	file, err := os.Stat(expectedExecPath)

	// file exists, is no dir: we assume it's a previously installed tf binary
	if err == nil && !file.IsDir() {
		bin.logger.Printf("Using existing terraform binaries: %s\n", expectedExecPath)
		return tfexec.NewTerraform(workingDir, expectedExecPath)

	} else {
		// in other case we reset this version and re-install
		versionInstallPath := filepath.Join(bin.dir, ver)
		err = os.RemoveAll(versionInstallPath)
		if err != nil {
			return nil, err
		}
		err = os.MkdirAll(versionInstallPath, 0777)
		if err != nil {
			return nil, err
		}

		maxTFVersion := version.Must(version.NewVersion("1.5.5"))
		expectedVersion := version.Must(version.NewVersion(ver))

		execPath := ""

		// if expected version is lower or equal to maximal supported TF version:
		if expectedVersion.Compare(maxTFVersion) < 1 {

			// install TF binaries
			ev := &releases.ExactVersion{
				Product:          product.Terraform,
				Version:          expectedVersion,
				InstallDir:       versionInstallPath,
				ArmoredPublicKey: hashicorpSecurityArmoredPublicKey,
			}

			execPath, err = ev.Install(ctx)
			if err != nil {
				return nil, err
			}

			bin.logger.Printf("Installed new terraform binaries to %s\n", expectedExecPath)
		} else {

			// install tofu binaries
			err = bin.installTofuBinaries(ctx, ver, versionInstallPath)
			if err != nil {
				return nil, err
			}

			bin.logger.Printf("Installed new tofu binaries to %s\n", expectedExecPath)
			execPath = expectedExecPath
		}

		return tfexec.NewTerraform(workingDir, execPath)
	}
}

func (bin *TfBinaries) installTofuBinaries(ctx context.Context, ver, dir string) (err error) {
	dl, err := tofudl.New()
	if err != nil {
		return err
	}
	// B8 fix (phase 2b): honor the caller's ctx instead of context.Background(), so the
	// configured init timeout (or run cancellation) actually bounds this network download
	// like it does every other step.
	tofuBinary, err := dl.Download(ctx, tofudl.DownloadOptVersion(tofudl.Version(ver)))
	if err != nil {
		return err
	}
	terraformBin := filepath.Join(dir, product.Terraform.BinaryName())
	if err := os.WriteFile(terraformBin, tofuBinary, 0700); err != nil {
		return err
	}
	// Note that the downloaded tofu binary is written to a file named 'terraform'
	// in order to stay compatible to the tfexec library which is used to actually run 'terraform apply' etc. (but now with compatible tofu sneaked in)
	// The symlink here though provides the original 'tofu' binary as well, as this is the actually correct binary name to be used in the PreRunScript,
	// see RunScript
	if err := os.Symlink(terraformBin, filepath.Join(dir, "tofu")); err != nil {
		return err
	}
	return nil
}
