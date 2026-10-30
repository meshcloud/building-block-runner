package runnerproc

import (
	"cmp"
	"time"
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

type ApiKey struct {
	ClientId     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

type Config struct {
	Options

	Binary     string
	Dir        string
	Uuid       string
	Endpoint   string
	Workspace  string
	Key        ApiKey
	KeyPair    KeyPair
	CoverDir   string
	TfCacheDir string
}

func (c Config) runnerConfig() map[string]any {
	return map[string]any{
		"uuid": c.Uuid,
		"meshstack": map[string]any{
			"endpoint":  cmp.Or(c.ApiUrl, c.Endpoint),
			"workspace": c.Workspace,
			"apiKey":    map[string]string{"clientId": c.Key.ClientId, "clientSecret": c.Key.ClientSecret},
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
