package config

import "github.com/meshcloud/building-block-runner/internal/config/xurl"

type Api struct {
	Url  xurl.URL `yaml:"url" env:"URL"`
	Auth Auth     `yaml:"auth" envPrefix:"AUTH_"`
}

type Auth struct {
	// Profile is a meshstack-cli profile, which supplies the url, the auth and the workspace that the config leaves empty.
	Profile   string `yaml:"profile" env:"PROFILE"`
	Workspace string `yaml:"workspace" env:"WORKSPACE"`
	// Token is the run token, which a k8s job gets from the dispatcher.
	Token string `yaml:"-" env:"TOKEN" envFallback:"MESHSTACK_API_TOKEN"`
	Basic struct {
		Username string `yaml:"username" env:"USERNAME" envFallback:"RUNNER_API_USERNAME"`
		Password string `yaml:"password" env:"PASSWORD" envFallback:"RUNNER_API_PASSWORD"`
	} `yaml:"basic" envPrefix:"BASIC_"`
	Key struct {
		ClientId     string `yaml:"clientId" env:"CLIENT_ID" envFallback:"RUNNER_API_CLIENT_ID"`
		ClientSecret string `yaml:"clientSecret" env:"CLIENT_SECRET" envFallback:"RUNNER_API_CLIENT_SECRET"`
	} `yaml:"key" envPrefix:"KEY_"`
}
