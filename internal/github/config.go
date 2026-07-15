package github

import (
	"fmt"

	"github.com/meshcloud/building-block-runner/internal/config"
)

// defaultMaxConcurrentRuns is the shipped default. It stays 1 for github
// because the workflow-run correlation window is heuristic and concurrent dispatches of the
// same workflow file can cross-track: a higher default makes mis-association
// more likely, so operators opt into more concurrency explicitly.
const defaultMaxConcurrentRuns = 1

// Config is the github type configuration. GitHub coordinates (base URL, owner,
// app id/pem, repo, branch, workflows) are per-run data in the implementation object, NOT
// runner config, so there are no github-specific config keys — only the shared meshfed
// API/auth section, the private key (this runner decrypts, unlike manual), and the uniform
// runner extras. The zero value is not usable in polling mode; build via LoadConfig and
// Validate.
type Config struct {
	Uuid    string
	Version string
	Api     config.Api
	// PrivateKey is the resolved cert-based decryption key PEM (via config.ResolvePrivateKey).
	// Required in polling mode (this runner decrypts appPem + sensitive inputs); empty and
	// unused in single-run mode (NoOp decryptor).
	PrivateKey        string
	MaxConcurrentRuns int
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
