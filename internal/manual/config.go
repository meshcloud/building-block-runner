package manual

import (
	"fmt"

	"github.com/meshcloud/building-block-runner/internal/config"
)

// defaultMaxConcurrentRuns is the standalone in-process concurrency default for the
// phase-6 personas (plan 05 §5): a modest throughput improvement over the former serial
// single-worker cadence, overridable via RUNNER_MAX_CONCURRENT_RUNS.
const defaultMaxConcurrentRuns = 3

// Config is the manual persona's configuration: the shared meshfed API/auth section plus
// the manual-only extras. The zero value is not usable in polling mode — construct it
// through the persona loader (cmd/manual) and Validate before use (P8).
type Config struct {
	// Uuid is this runner's uuid (RUNNER_UUID / blockrunner.uuid), used as the claim
	// forRunnerUuid, the status-source id, and the node-id header.
	Uuid string
	// Version stamps the X-Meshcloud-Runner-Version header. It defaults to the ldflags build
	// version but VERSION (env) / blockrunner.version override it (§6.2, flag §16.6).
	Version string
	// Api is the shared meshfed connection/auth (url + Basic/API-key), config.Api.
	Api config.Api
	// DebugMode swaps in the dev-only debug execution path (blockrunner.debugMode).
	DebugMode bool
	// MaxConcurrentRuns caps in-process concurrent runs in polling mode (default 3).
	MaxConcurrentRuns int
}

// Validate fails fast (P5) on an unusable polling-mode config. In single-run mode the run
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
