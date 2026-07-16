package manual

import (
	"fmt"

	"github.com/meshcloud/building-block-runner/internal/config"
)

// Config is the manual type's configuration: the dispatcher-owned shared section
// (config.BaseConfig -- uuid, api, maxConcurrentRuns) plus the manual-only extras. The zero
// value is not usable in polling mode — construct it through LoadConfig and Validate before use.
type Config struct {
	config.BaseConfig
	// Version stamps the X-Meshcloud-Runner-Version header. It defaults to the ldflags build
	// version but VERSION (env) / blockrunner.version override it.
	Version string
	// DebugMode swaps in the dev-only debug execution path (blockrunner.debugMode).
	DebugMode bool
}

// Validate fails fast on an unusable polling-mode config. In single-run mode the run
// token carries auth and the run arrives from a file, so uuid/api/auth are not required
// (the tf single-run exemption, config.Api.Validate(required=false)).
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
	return c.Api.Validate("api", true)
}
