package config

import (
	"fmt"

	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

// Shared compiled-in dev-local defaults for every fit runner type. meshStack's local-dev
// stack knows exactly one "magic runner" identity, and every type talks to the same meshfed-API
// endpoint, so these values are identical across types and live here once instead of being
// re-declared in each type's loadconfig.go. Real deployments override them via RUNNER_UUID /
// RUNNER_API_URL / RUNNER_API_USERNAME / RUNNER_API_PASSWORD.
const (
	// DefaultRunnerUuid is the single well-known local-dev "magic runner" uuid (meshStack knows
	// no other local uuid); every fit type's compiled-in default points here.
	DefaultRunnerUuid = "98520496-627d-43e6-82da-ce499179ff3f"

	DefaultApiUsername = "bb-api"
	DefaultApiPassword = "guest"

	// DefaultApiUrl points straight at the local meshfed-API (:8080), bypassing the per-type dev
	// mux -- the superset and every single-type dispatcher default to this one endpoint.
	DefaultApiUrl = "http://localhost:8080"
)

// Api is the shared meshfed-API connection/auth section. The Basic-auth username uses the
// canonical `username:` yaml key across every runner type (the legacy tf-only `user:` alias was
// dropped in the pre-release consolidation -- see docs/DEPRECATIONS.md).
type Api struct {
	Url          string `yaml:"url"`
	Username     string `yaml:"username"`
	Password     string `yaml:"password"`
	ClientId     string `yaml:"clientId"`
	ClientSecret string `yaml:"clientSecret"`
}

// NewAuthProvider returns the appropriate meshapi.AuthProvider for the configured
// credentials: API key auth wins when clientId+clientSecret are both set, Basic auth
// when username+password are both set, else nil (tf semantics -- a nil provider
// is valid in single-run mode, where the run token carries auth; the controller's
// former unconditional-BasicAuth fallback is unreachable once Validate(required=true)
// has already rejected an incomplete config, so nil here changes nothing observable).
//
// fallbackURL is used when Url itself is unset (e.g. a per-implementation override that
// only carries clientId/clientSecret and reuses the top-level api.url).
func (a Api) NewAuthProvider(fallbackURL string) meshapi.AuthProvider {
	url := a.Url
	if url == "" {
		url = fallbackURL
	}
	if a.ClientId != "" && a.ClientSecret != "" {
		return meshapi.NewApiKeyAuth(url, a.ClientId, a.ClientSecret)
	}
	if a.Username != "" && a.Password != "" {
		return meshapi.BasicAuth{Username: a.Username, Password: a.Password}
	}
	return nil
}

// Validate checks that at least one complete auth method is configured, reproducing the
// controller's per-field error messages (run-controller/controller/config.go
// validateApiAuth) verbatim. context is a human-readable field-path prefix (e.g. "api").
// required=false is the tf single-run exemption (tf-block-runner/tfrun/config.go
// validateAuthConfig): a run-token-only single-run invocation needs no api-level auth at
// all, so an incomplete/absent Api section is not an error in that mode.
func (a Api) Validate(context string, required bool) error {
	hasApiKey := a.ClientId != "" && a.ClientSecret != ""
	hasBasic := a.Username != "" && a.Password != ""

	if hasApiKey || hasBasic {
		return nil
	}
	if !required {
		return nil
	}

	if a.ClientId != "" || a.ClientSecret != "" {
		if a.ClientId == "" {
			return fmt.Errorf("%s.clientId is required when using API key auth", context)
		}
		return fmt.Errorf("%s.clientSecret is required when using API key auth", context)
	}
	if a.Username != "" || a.Password != "" {
		if a.Username == "" {
			return fmt.Errorf("%s.username is required when using Basic auth", context)
		}
		return fmt.Errorf("%s.password is required when using Basic auth", context)
	}
	return fmt.Errorf("%s: no authentication configured; set either username/password (Basic auth) or clientId/clientSecret (API key auth)", context)
}
