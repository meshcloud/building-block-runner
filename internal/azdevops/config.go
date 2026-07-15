package azdevops

import (
	"fmt"

	"github.com/meshcloud/building-block-runner/internal/config"
)

// defaultMaxConcurrentRuns is the standalone in-process concurrency default for the
// runner types, an intentional throughput improvement over azdevops's former
// single-threaded Kotlin scheduler -- a sync poll here can hold a worker slot for up
// to 30 minutes, so this default lets up to 3 such polls run concurrently instead of one at
// a time; RUNNER_MAX_CONCURRENT_RUNS=1 restores the old serial cadence.
const defaultMaxConcurrentRuns = 3

// Config is the azure-devops type's configuration: the shared meshfed API/auth section
// plus the azdevops-only extras. Construct it through LoadConfig and Validate before use --
// the zero value is not usable in polling mode (it decrypts PATs/inputs, so it needs
// a resolvable private key, unlike manual).
type Config struct {
	// Uuid is this runner's uuid (RUNNER_UUID / blockrunner.uuid).
	Uuid string
	// Version stamps the X-Meshcloud-Runner-Version header.
	Version string
	// Api is the shared meshfed connection/auth (url + Basic/API-key).
	Api config.Api
	// PrivateKey is the resolved PEM (config.ResolvePrivateKey) used to decrypt
	// the PAT and sensitive inputs in polling mode.
	PrivateKey string
	// MaxConcurrentRuns caps in-process concurrent runs in polling mode (default 3).
	MaxConcurrentRuns int
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
