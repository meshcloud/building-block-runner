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

type Binaries struct {
	dir        string
	abortGrace time.Duration
	logger     *log.Logger
	mu         sync.Mutex
}

func NewBinaries(dir string, abortGrace time.Duration, writer io.Writer) (*Binaries, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	return &Binaries{dir: dir, abortGrace: abortGrace, logger: log.New(writer, "[tf binaries] ", log.LstdFlags)}, nil
}

func (bin *Binaries) GetTF(ctx context.Context, workingDir string, ver string) (Facade, error) {
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
		if err := tf.SetWaitDelay(bin.abortGrace); err != nil {
			return nil, err
		}
	}
	return loggedFacade{tf}, nil
}

func (bin *Binaries) install(ctx context.Context, ver string) (string, error) {
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
