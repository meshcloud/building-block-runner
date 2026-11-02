package config

import (
	"context"

	"github.com/meshcloud/meshstack-cli/client/types/xurl"
	"github.com/meshcloud/meshstack-cli/pkg/setting"
)

// Api is the setting source of the meshStack client, so the MESHSTACK_* environment fills only what it leaves empty.
type Api struct {
	Url  xurl.URL `yaml:"url" env:"URL"`
	Auth Auth     `yaml:"auth"`
}

type Auth struct {
	Profile   string `yaml:"profile"`
	Workspace string `yaml:"workspace"`
	Key       ApiKey `yaml:"key"`
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
	switch settingKey {
	case setting.Endpoint.EnvKey():
		if api.Url.URL == nil {
			return "", "api.url"
		}
		return api.Url.String(), "api.url"
	case setting.Profile.EnvKey():
		return api.Auth.Profile, "api.auth.profile"
	case setting.Workspace.EnvKey():
		return api.Auth.Workspace, "api.auth.workspace"
	case setting.ApiKeyClientId.EnvKey():
		return api.Auth.Key.ClientId, "api.auth.key.clientId"
	case setting.ApiKeyClientSecret.EnvKey():
		return api.Auth.Key.ClientSecret, "api.auth.key.clientSecret"
	}
	return "", ""
}
