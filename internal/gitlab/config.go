package gitlab

import (
	"fmt"

	"github.com/meshcloud/building-block-runner/internal/config"
)

// defaultMaxConcurrentRuns is the standalone in-process concurrency default for the
// runner types, shared verbatim with the manual template.
const defaultMaxConcurrentRuns = 3

// Config is the gitlab type's configuration: the shared meshfed API/auth section plus
// the gitlab-only extras. Unlike manual, gitlab always needs a resolvable
// private key in polling mode (every run decrypts a pipeline trigger token) -- resolution
// happens in LoadConfig via config.ResolvePrivateKey, and PrivateKeyPEM below is already
// the resolved PEM content, not a path.
type Config struct {
	// Uuid is this runner's uuid (RUNNER_UUID / blockrunner.uuid).
	Uuid string
	// Version stamps the X-Meshcloud-Runner-Version header.
	Version string
	// Api is the shared meshfed connection/auth (url + Basic/API-key), config.Api.
	Api config.Api
	// PrivateKeyPEM is the resolved private-key PEM content (config.ResolvePrivateKey)
	// used to build the polling-mode cert-based Decryptor. Empty in single-run mode
	// (the NoOp decryptor is used there instead; the controller already decrypted).
	PrivateKeyPEM string
	// MaxConcurrentRuns caps in-process concurrent runs in polling mode (default 3).
	MaxConcurrentRuns int
}

// Validate fails fast on an unusable polling-mode config. In single-run mode the run
// token carries reporting auth and the controller already decrypted, so uuid/api/auth/key
// are not required (the tf/manual single-run exemption).
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
	if c.PrivateKeyPEM == "" {
		return fmt.Errorf("no private key configured; set RUNNER_PRIVATE_KEY_FILE, privateKeyFile, or privateKey " +
			"(every GitLab trigger decrypts the pipeline trigger token)")
	}
	return nil
}
