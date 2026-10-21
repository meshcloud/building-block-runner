package config

import (
	"context"

	"github.com/meshcloud/meshstack-cli/client/types/xurl"
	"github.com/meshcloud/meshstack-cli/pkg/setting"
)

type Meshstack struct {
	Endpoint  xurl.URL `yaml:"endpoint"`
	Profile   string   `yaml:"profile"`
	Workspace string   `yaml:"workspace"`
	ApiKey    struct {
		ClientId     string `yaml:"clientId"`
		ClientSecret string `yaml:"clientSecret"`
	} `yaml:"apiKey"`
}

func (m Meshstack) Lookup(_ context.Context, settingKey string) (string, error) {
	value, _ := m.setting(settingKey)
	return value, nil
}

func (m Meshstack) Describe(settingKey string) string {
	if _, path := m.setting(settingKey); path != "" {
		return "runner config " + path
	}
	return ""
}

func (m Meshstack) setting(settingKey string) (value, path string) {
	switch settingKey {
	case setting.Endpoint.EnvKey():
		if m.Endpoint.URL == nil {
			return "", "meshstack.endpoint"
		}
		return m.Endpoint.String(), "meshstack.endpoint"
	case setting.Profile.EnvKey():
		return m.Profile, "meshstack.profile"
	case setting.Workspace.EnvKey():
		return m.Workspace, "meshstack.workspace"
	case setting.ApiKeyClientId.EnvKey():
		return m.ApiKey.ClientId, "meshstack.apiKey.clientId"
	case setting.ApiKeyClientSecret.EnvKey():
		return m.ApiKey.ClientSecret, "meshstack.apiKey.clientSecret"
	}
	return "", ""
}
