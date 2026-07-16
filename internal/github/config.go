package github

import (
	"fmt"

	"github.com/meshcloud/building-block-runner/internal/config"
)

// Config is the github type configuration: the dispatcher-owned shared section
// (config.BaseConfig -- uuid, api, maxConcurrentRuns) plus the github-only extras. GitHub
// coordinates (base URL, owner, app id/pem, repo, branch, workflows) are per-run data in the
// implementation object, NOT runner config, so there are no github-specific connection keys —
// only the version header and the private key (this runner decrypts, unlike manual). The zero
// value is not usable in polling mode; build via LoadConfig and Validate.
type Config struct {
	config.BaseConfig
	Version string
	// PrivateKey is the resolved cert-based decryption key PEM (via config.ResolvePrivateKey).
	// Required in polling mode (this runner decrypts appPem + sensitive inputs); empty and
	// unused in single-run mode (NoOp decryptor).
	PrivateKey string
}

// Validate fails fast on an unusable polling-mode config. Unlike manual, a resolvable
// private key is required in polling mode because the handler decrypts appPem and sensitive
// inputs — a missing key is a startup failure, not a first-run failure. Single-run mode is
// exempt (auth via runToken, decryption already done by the controller).
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
		return fmt.Errorf("a private key is required in polling mode (this runner decrypts appPem and sensitive inputs); " +
			"set RUNNER_PRIVATE_KEY_FILE, blockrunner.privateKeyFile, or an inline blockrunner.privateKey")
	}
	return nil
}
