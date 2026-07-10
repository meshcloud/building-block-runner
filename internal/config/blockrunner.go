package config

import "log/slog"

// BlockRunnerCompat is the Kotlin-era `blockrunner:` yaml surface every published runner
// image accepted (StandaloneBlockRunnerApiConfig / ManualRunnerConfig, block-runner-core).
// Customers mount their existing runner-config.yml onto the new Go images, so the persona
// loaders keep accepting this block and normalize it into their flat Go-native config
// after load (umbrella §5.4/§4 row 11, 06A §6.4). Zero-value fields mean "not present".
//
// Precedence within the file layer is defaults < flat Go-native keys < this block < env:
// a value set in the `blockrunner:` block overrides the flat key (a mounted Kotlin-era
// file must fully configure the persona), and env bindings applied afterwards still win.
type BlockRunnerCompat struct {
	Uuid      string `yaml:"uuid"`
	Version   string `yaml:"version"`
	DebugMode *bool  `yaml:"debugMode"` // manual-only; other personas warn-and-ignore
	Api       struct {
		Url string `yaml:"url"`
	} `yaml:"api"`
	Auth struct {
		Username string `yaml:"username"`
		Password string `yaml:"password"`
		ApiKey   struct {
			ClientId     string `yaml:"client-id"`
			ClientSecret string `yaml:"client-secret"`
		} `yaml:"api-key"`
	} `yaml:"auth"`
	PrivateKey     string `yaml:"privateKey"`     // consumed by the 06B–D personas
	PrivateKeyFile string `yaml:"privateKeyFile"` // consumed by the 06B–D personas
}

// ApplyShared normalizes the cross-persona fields (uuid, version, api url/auth) of the
// `blockrunner:` block onto the flat targets, deprecation-logging each field it applies.
// Only non-empty block fields override — an unset block field never clears a flat/default
// value. DebugMode and the private-key fields are persona-specific and are read directly
// off the struct by the personas that use them (manual: DebugMode; 06B–D: PrivateKey*).
func (c BlockRunnerCompat) ApplyShared(log *slog.Logger, uuid, version *string, api *Api) {
	apply := func(key, canonical string, target *string, val string) {
		if val == "" || target == nil {
			return
		}
		WarnDeprecated(log, key, canonical)
		*target = val
	}

	apply("blockrunner.uuid", "uuid", uuid, c.Uuid)
	apply("blockrunner.version", "version", version, c.Version)
	if api != nil {
		apply("blockrunner.api.url", "api.url", &api.Url, c.Api.Url)
		apply("blockrunner.auth.username", "api.username", &api.Username, c.Auth.Username)
		apply("blockrunner.auth.password", "api.password", &api.Password, c.Auth.Password)
		apply("blockrunner.auth.api-key.client-id", "api.clientId", &api.ClientId, c.Auth.ApiKey.ClientId)
		apply("blockrunner.auth.api-key.client-secret", "api.clientSecret", &api.ClientSecret, c.Auth.ApiKey.ClientSecret)
	}
}
