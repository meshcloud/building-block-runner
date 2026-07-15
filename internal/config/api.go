package config

import (
	"fmt"

	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

// Api is the shared meshfed-API connection/auth section. Both yaml spellings for
// the Basic-auth username keep working: tf uses `user` (tf-block-runner/tfrun/config.go),
// the controller uses `username` (run-controller/controller/config.go) -- username() below
// normalizes the two into one value without renaming either yaml key (no alias table
// growth).
type Api struct {
	Url          string `yaml:"url"`
	Username     string `yaml:"username"`
	User         string `yaml:"user"` // alias of Username; normalized by username()
	Password     string `yaml:"password"`
	ClientId     string `yaml:"clientId"`
	ClientSecret string `yaml:"clientSecret"`
}

// username resolves the Basic-auth username regardless of which yaml key set it.
// Username wins when both are set (matches decode order: whichever key appears last in
// the merged document is a YAML-level concern, not this method's).
func (a Api) username() string {
	if a.Username != "" {
		return a.Username
	}
	return a.User
}

// NewAuthProvider returns the appropriate meshapi.AuthProvider for the configured
// credentials: API key auth wins when clientId+clientSecret are both set, Basic auth
// when username(alias)+password are both set, else nil (tf semantics -- a nil provider
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
	if a.username() != "" && a.Password != "" {
		return meshapi.BasicAuth{Username: a.username(), Password: a.Password}
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
	hasBasic := a.username() != "" && a.Password != ""

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
	if a.username() != "" || a.Password != "" {
		if a.username() == "" {
			return fmt.Errorf("%s.username is required when using Basic auth", context)
		}
		return fmt.Errorf("%s.password is required when using Basic auth", context)
	}
	return fmt.Errorf("%s: no authentication configured; set either username/password (Basic auth) or clientId/clientSecret (API key auth)", context)
}
