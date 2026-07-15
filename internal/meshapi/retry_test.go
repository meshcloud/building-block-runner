package meshapi

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// The generic retry/backoff mechanics (Retry-After handling, whitelist-by-suffix, body
// replay, context cancellation, ...) are exercised in internal/httpclient's own test suite
// now that the mechanism lives there. This test pins meshapi's single retry *policy*:
// the whitelist and its budget/backoff. End-to-end retry-through coverage of the production
// wiring lives in consolidation_test.go (TestRunClient_RegisterSource_RetriesThrough503) and
// coverage_extra_test.go (TestNewApiKeyAuth_WrapsRetryTransport).

func TestGlobalRetryOptions(t *testing.T) {
	opts := globalRetryOptions()
	assert.Equal(t, 12, opts.MaxRetries)
	assert.Equal(t, []string{"/status/source", "/api/login", "/access_tokens"}, opts.WhitelistedPosts,
		"register-source (409-on-replay treated as success), login and github's installation-token "+
			"mint (both idempotent token mints) are the only safe-to-replay POSTs; the claim POST, "+
			"status PATCH and the CI trigger POSTs stay absent")

	assert.Equal(t, 1*time.Second, opts.Backoff.Wait(1))
	assert.Equal(t, 30*time.Second, opts.Backoff.Wait(100), "capped at MaxWait")
}
