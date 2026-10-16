package config

import (
	"context"

	"github.com/meshcloud/meshstack-cli/pkg/setting"

	"github.com/meshcloud/building-block-runner/internal/config/xurl"
)

// Api is the setting source of the meshStack client, so the MESHSTACK_* environment fills only what it leaves empty.
type Api struct {
	Url  xurl.URL `yaml:"url" env:"URL"`
	Auth Auth     `yaml:"auth"`
}

// Auth reads the legacy RUNNER_API_* names from the environment.
type Auth struct {
	Profile   string    `yaml:"profile"`
	Workspace string    `yaml:"workspace"`
	Basic     BasicAuth `yaml:"basic"`
	Key       ApiKey    `yaml:"key"`
}

type BasicAuth struct {
	Username string `yaml:"username" env:"USERNAME"`
	Password string `yaml:"password" env:"PASSWORD"`
}

type ApiKey struct {
	ClientId     string `yaml:"clientId" env:"CLIENT_ID"`
	ClientSecret string `yaml:"clientSecret" env:"CLIENT_SECRET"`
}

func (api Api) Lookup(_ context.Context, settingKey string) (string, error) {
	value, _ := api.setting(settingKey)
	return value, nil
}

func (api Api) Describe(settingKey string) string {
	if _, path := api.setting(settingKey); path != "" {
		return "runner config " + path
	}
	return ""
}

func (api Api) setting(settingKey string) (value, path string) {
	auth := api.Auth
	// The legacy run-controller config holds basic auth, which deployments override with a key from the environment.
	if auth.Key.ClientId != "" {
		auth.Basic = BasicAuth{}
	}
	switch settingKey {
	case setting.Endpoint.EnvKey():
		return api.Url.String(), "api.url"
	case setting.Profile.EnvKey():
		return auth.Profile, "api.auth.profile"
	case setting.Workspace.EnvKey():
		return auth.Workspace, "api.auth.workspace"
	case setting.ApiKeyClientId.EnvKey():
		return auth.Key.ClientId, "api.auth.key.clientId"
	case setting.ApiKeyClientSecret.EnvKey():
		return auth.Key.ClientSecret, "api.auth.key.clientSecret"
	case setting.ApiUsername.EnvKey():
		return auth.Basic.Username, "api.auth.basic.username"
	case setting.ApiPassword.EnvKey():
		return auth.Basic.Password, "api.auth.basic.password"
	}
	return "", ""
}
