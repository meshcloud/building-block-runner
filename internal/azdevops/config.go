package azdevops

import (
	"fmt"

	"github.com/meshcloud/building-block-runner/internal/config"
)

// Config is the azure-devops type's configuration: the dispatcher-owned shared section
// (config.BaseConfig -- uuid, api, maxConcurrentRuns) plus the azdevops-only extras. Construct it
// through LoadConfig and Validate before use -- the zero value is not usable in polling mode (it
// decrypts PATs/inputs, so it needs a resolvable private key, unlike manual). The shared
// concurrency default (3) is an intentional throughput improvement over azdevops's former
// single-threaded Kotlin scheduler -- a sync poll here can hold a worker slot for up to 30
// minutes, so up to 3 such polls run concurrently; RUNNER_MAX_CONCURRENT_RUNS=1 restores the old
// serial cadence.
type Config struct {
	config.BaseConfig
	// Version stamps the X-Meshcloud-Runner-Version header.
	Version string
	// PrivateKey is the resolved PEM (config.ResolvePrivateKey) used to decrypt
	// the PAT and sensitive inputs in polling mode.
	PrivateKey string
}

// Validate fails fast on an unusable polling-mode config. In single-run mode the
// controller has already decrypted everything and the run token carries auth, so
// uuid/api/auth/key are not required (the tf/manual single-run exemption).
func (c Config) Validate(singleRun bool) error {
	if singleRun {
		return nil
	}
	if c.Uuid == "" {
		return fmt.Errorf("uuid is required")
	}
	if c.Api.Url == "" {
		return fmt.Errorf("api.url is required")
	}
	if err := c.Api.Validate("api", true); err != nil {
		return err
	}
	if c.PrivateKey == "" {
		return fmt.Errorf("a resolvable private key is required in polling mode (set RUNNER_PRIVATE_KEY_FILE, blockrunner.privateKeyFile, or blockrunner.privateKey) -- azure devops decrypts the runner typel access token and sensitive inputs")
	}
	return nil
}
