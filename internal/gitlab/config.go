package gitlab

import (
	"fmt"

	"github.com/meshcloud/building-block-runner/internal/config"
)

// Config is the gitlab type's configuration: the dispatcher-owned shared section
// (config.BaseConfig -- uuid, api, maxConcurrentRuns) plus the gitlab-only extras. Unlike manual,
// gitlab always needs a resolvable private key in polling mode (every run decrypts a pipeline
// trigger token) -- resolution happens in LoadConfig via config.ResolvePrivateKey, and
// PrivateKeyPEM below is already the resolved PEM content, not a path.
type Config struct {
	config.BaseConfig
	// Version stamps the X-Meshcloud-Runner-Version header.
	Version string
	// PrivateKeyPEM is the resolved private-key PEM content (config.ResolvePrivateKey)
	// used to build the polling-mode cert-based Decryptor. Empty in single-run mode
	// (the NoOp decryptor is used there instead; the controller already decrypted).
	PrivateKeyPEM string
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
