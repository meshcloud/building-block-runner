package tf

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/gofrs/flock"
	"github.com/hashicorp/go-version"
	"github.com/hashicorp/hc-install/product"
	"github.com/hashicorp/hc-install/releases"
	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/opentofu/tofudl"
)

const (
	installLockRetryDelay = 500 * time.Millisecond
	tofuBinaryName        = "tofu"
)

var lastTerraformVersion = version.Must(version.NewVersion("1.5.5"))

//go:embed hashicorp-public-key.asc
var hashicorpSecurityArmoredPublicKey string

type TfBinaries struct {
	dir       string
	stopGrace time.Duration
	logger    *log.Logger
	download  func(ctx context.Context, ver, dir string) (binaryName string, err error)
}

func NewTfBin(dir string, stopGrace time.Duration, writer io.Writer) (*TfBinaries, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	return &TfBinaries{dir: dir, stopGrace: stopGrace, logger: log.New(writer, "[TfBinaries] ", log.LstdFlags), download: download}, nil
}

func (bin *TfBinaries) GetTF(ctx context.Context, workingDir string, ver string) (TfFacade, error) {
	unlock, err := bin.lock(ctx, ver)
	if err != nil {
		return nil, err
	}
	defer unlock()

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

func (bin *TfBinaries) lock(ctx context.Context, ver string) (unlock func(), err error) {
	//goland:noinspection GoResourceLeak // the returned unlock func closes the lock.
	lock := flock.New(filepath.Join(bin.dir, ver+".lock"))
	if _, err := lock.TryLockContext(ctx, installLockRetryDelay); err != nil {
		return nil, fmt.Errorf("lock the install dir of tf %s: %w", ver, err)
	}
	return func() {
		if err := lock.Close(); err != nil {
			bin.logger.Printf("Unlocking the install dir of tf %s failed: %v\n", ver, err)
		}
	}, nil
}

func (bin *TfBinaries) install(ctx context.Context, ver string) (string, error) {
	versionDir := filepath.Join(bin.dir, ver)
	if _, err := os.Stat(versionDir); err == nil {
		execPath := installedBinary(versionDir)
		bin.logger.Printf("Using the installed binary %s\n", execPath)
		return execPath, nil
	}

	if err := bin.removeInterruptedInstalls(ver); err != nil {
		return "", err
	}
	tmpDir, err := os.MkdirTemp(bin.dir, ver+".tmp-")
	if err != nil {
		return "", err
	}
	defer func() {
		if removeErr := os.RemoveAll(tmpDir); removeErr != nil {
			bin.logger.Printf("Removing %s failed: %v\n", tmpDir, removeErr)
		}
	}()

	binaryName, err := bin.download(ctx, ver, tmpDir)
	if err != nil {
		return "", fmt.Errorf("download tf %s: %w", ver, err)
	}
	if err := os.Rename(tmpDir, versionDir); err != nil {
		return "", err
	}
	execPath := filepath.Join(versionDir, binaryName)
	bin.logger.Printf("Installed %s\n", execPath)
	return execPath, nil
}

// The older layout named the tofu binary terraform and linked tofu to it.
func installedBinary(versionDir string) string {
	tofuPath := filepath.Join(versionDir, tofuBinaryName)
	if _, err := os.Stat(tofuPath); err == nil {
		return tofuPath
	}
	return filepath.Join(versionDir, product.Terraform.BinaryName())
}

func (bin *TfBinaries) removeInterruptedInstalls(ver string) error {
	leftovers, err := filepath.Glob(filepath.Join(bin.dir, ver+".tmp-*"))
	if err != nil {
		return err
	}
	for _, leftover := range leftovers {
		bin.logger.Printf("Removing the interrupted install %s\n", leftover)
		if err := os.RemoveAll(leftover); err != nil {
			return err
		}
	}
	return nil
}

func download(ctx context.Context, ver, dir string) (string, error) {
	expectedVersion, err := version.NewVersion(ver)
	if err != nil {
		return "", err
	}
	if expectedVersion.GreaterThan(lastTerraformVersion) {
		return tofuBinaryName, downloadTofu(ctx, ver, dir)
	}
	ev := &releases.ExactVersion{
		Product:          product.Terraform,
		Version:          expectedVersion,
		InstallDir:       dir,
		ArmoredPublicKey: hashicorpSecurityArmoredPublicKey,
	}
	if _, err := ev.Install(ctx); err != nil {
		return "", err
	}
	return product.Terraform.BinaryName(), nil
}

func downloadTofu(ctx context.Context, ver, dir string) error {
	dl, err := tofudl.New()
	if err != nil {
		return err
	}
	tofuBinary, err := dl.Download(ctx, tofudl.DownloadOptVersion(tofudl.Version(ver)))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, tofuBinaryName), tofuBinary, 0o700); err != nil { //nolint:gosec // the tofu binary must be executable.
		return err
	}
	// Legacy pre-run scripts call terraform; the relative target survives the rename of dir.
	return os.Symlink(tofuBinaryName, filepath.Join(dir, product.Terraform.BinaryName()))
}
