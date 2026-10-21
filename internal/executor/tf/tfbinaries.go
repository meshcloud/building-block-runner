package tf

import (
	"context"
	_ "embed"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/hashicorp/go-version"
	"github.com/hashicorp/hc-install/product"
	"github.com/hashicorp/hc-install/releases"
	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/opentofu/tofudl"
)

//go:embed hashicorp-public-key.asc
var hashicorpSecurityArmoredPublicKey string

type TfBinaries struct {
	dir       string
	stopGrace time.Duration
	logger    *log.Logger
	mu        sync.Mutex
}

func NewTfBin(dir string, stopGrace time.Duration, writer io.Writer) (*TfBinaries, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	return &TfBinaries{dir: dir, stopGrace: stopGrace, logger: log.New(writer, "[TfBinaries] ", log.LstdFlags)}, nil
}

func (bin *TfBinaries) GetTF(ctx context.Context, workingDir string, ver string) (TfFacade, error) {
	bin.mu.Lock()
	defer bin.mu.Unlock()

	execPath, err := bin.install(ctx, ver)
	if err != nil {
		return nil, err
	}
	tf, err := tfexec.NewTerraform(workingDir, execPath)
	if err != nil {
		return nil, err
	}
	// tfexec has no graceful stop on windows and refuses a wait delay there.
	if runtime.GOOS != "windows" {
		if err := tf.SetWaitDelay(bin.stopGrace); err != nil {
			return nil, err
		}
	}
	return tf, nil
}

func (bin *TfBinaries) install(ctx context.Context, ver string) (string, error) {
	versionInstallPath := filepath.Join(bin.dir, ver)
	expectedExecPath := filepath.Join(versionInstallPath, product.Terraform.BinaryName())
	if file, err := os.Stat(expectedExecPath); err == nil && !file.IsDir() {
		bin.logger.Printf("Using existing terraform binaries: %s\n", expectedExecPath)
		return expectedExecPath, nil
	}

	if err := os.RemoveAll(versionInstallPath); err != nil {
		return "", err
	}
	if err := os.MkdirAll(versionInstallPath, 0o750); err != nil {
		return "", err
	}

	maxTFVersion := version.Must(version.NewVersion("1.5.5"))
	expectedVersion, err := version.NewVersion(ver)
	if err != nil {
		return "", err
	}
	if expectedVersion.Compare(maxTFVersion) > 0 {
		if err = installTofuBinaries(ctx, ver, versionInstallPath); err != nil {
			return "", err
		}
		bin.logger.Printf("Installed new tofu binaries to %s\n", expectedExecPath)
		return expectedExecPath, nil
	}

	ev := &releases.ExactVersion{
		Product:          product.Terraform,
		Version:          expectedVersion,
		InstallDir:       versionInstallPath,
		ArmoredPublicKey: hashicorpSecurityArmoredPublicKey,
	}
	execPath, err := ev.Install(ctx)
	if err != nil {
		return "", err
	}
	bin.logger.Printf("Installed new terraform binaries to %s\n", execPath)
	return execPath, nil
}

func installTofuBinaries(ctx context.Context, ver, dir string) error {
	dl, err := tofudl.New()
	if err != nil {
		return err
	}
	tofuBinary, err := dl.Download(ctx, tofudl.DownloadOptVersion(tofudl.Version(ver)))
	if err != nil {
		return err
	}
	terraformBin := filepath.Join(dir, product.Terraform.BinaryName())
	if err := os.WriteFile(terraformBin, tofuBinary, 0o700); err != nil { //nolint:gosec // the tofu binary must be executable.
		return err
	}
	// tfexec runs the binary named terraform, and the pre-run script calls it as tofu.
	return os.Symlink(terraformBin, filepath.Join(dir, "tofu"))
}
