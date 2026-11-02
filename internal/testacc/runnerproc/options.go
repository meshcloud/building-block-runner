package runnerproc

import (
	"time"

	"github.com/meshcloud/building-block-runner/v2/internal/config"
)

const (
	RunTimeout   = 10 * time.Minute
	PollInterval = 2 * time.Second
)

type Options struct {
	ForType string
	// Timeout is the dispatcher's default when empty.
	Timeout          string
	RegisterAs       string
	InsecureHostKeys bool
	MetricsPort      int
	ApiUrl           string
	KeycloakWif      map[string]any
	SshKnownHosts    string
}

type Config struct {
	Options

	Binary     string
	Dir        string
	Uuid       string
	Endpoint   string
	Workspace  string
	Key        config.ApiKey
	KeyPair    KeyPair
	CoverDir   string
	TfCacheDir string
}

func (c Config) runnerConfig() map[string]any {
	return map[string]any{
		"uuid": c.Uuid,
		"api": map[string]any{
			"url": c.Endpoint,
			"auth": map[string]any{
				"workspace": c.Workspace,
				"key":       map[string]any{"clientId": c.Key.ClientId, "clientSecret": c.Key.ClientSecret},
			},
		},
		"crypto":     map[string]any{"privateKeys": []string{c.KeyPair.PrivateKeyPem}},
		"dispatcher": c.dispatcher(),
		"tf":         map[string]any{"cacheDir": c.TfCacheDir, "insecureHostKeys": c.InsecureHostKeys},
	}
}

func (o Options) dispatcher() map[string]any {
	dispatcher := map[string]any{"forTypes": []string{o.ForType}, "pollingInterval": "1s"}
	if o.Timeout != "" {
		dispatcher["timeout"] = o.Timeout
	}
	if o.RegisterAs != "" {
		dispatcher["register"] = map[string]any{"displayName": o.RegisterAs}
	}
	if o.KeycloakWif != nil {
		dispatcher["keycloakWif"] = o.KeycloakWif
	}
	return dispatcher
}
