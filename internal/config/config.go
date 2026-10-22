package config

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"uuid"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"

	"github.com/meshcloud/building-block-runner/internal/config/internal/envsubst"
	"github.com/meshcloud/building-block-runner/internal/config/internal/legacy"
	"github.com/meshcloud/building-block-runner/internal/config/tf"
	"github.com/meshcloud/building-block-runner/internal/config/xtime"
	"github.com/meshcloud/building-block-runner/internal/crypto"
)

type Config struct {
	Uuid uuid.UUID `yaml:"uuid" env:"UUID"`

	Api        Api           `yaml:"api" envPrefix:"API_"`
	Crypto     crypto.Crypto `yaml:"crypto" envPrefix:"CRYPTO_"`
	Dispatcher Dispatcher    `yaml:"dispatcher" envPrefix:"DISPATCHER_"`

	K8s *K8s `yaml:"k8s" envPrefix:"K8S_"`

	Tf tf.Config `yaml:"tf" envPrefix:"TF_"`
}

func defaultConfig() Config {
	return Config{Dispatcher: defaultDispatcher(), Tf: tf.Default()}
}

func (cfg *Config) RunnerDir() string {
	return filepath.Join(os.TempDir(), "bb-runner-"+cfg.Uuid.String())
}

func (cfg *Config) defaultTfCacheDir() string {
	return filepath.Join(cfg.RunnerDir(), "tfcache")
}

func FileFromEnv() string {
	return cmp.Or(os.Getenv("RUNNER_CONFIG_FILE"), os.Getenv("RUNCONTROLLER_CONFIG_FILE"), "runner-config.yml")
}

func LoadFile(ctx context.Context, path string) (Config, error) {
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		slog.InfoContext(ctx, "config file does not exist, using defaults and environment", "path", path)
	case err != nil:
		return Config{}, err
	}
	cfg, err := Load(ctx, data)
	if err != nil {
		return Config{}, fmt.Errorf("config file %s: %w", path, err)
	}
	return cfg, nil
}

func Load(ctx context.Context, data []byte) (Config, error) {
	file, err := parser.ParseBytes(data, 0)
	if err != nil {
		return Config{}, err
	}
	if len(file.Docs) > 1 {
		return Config{}, fmt.Errorf("config has %d yaml documents, expected one", len(file.Docs))
	}
	cfg := defaultConfig()
	if len(file.Docs) == 1 && file.Docs[0].Body != nil {
		if err := cfg.decode(ctx, file.Docs[0].Body); err != nil {
			return Config{}, err
		}
	}
	if err := applyEnvironment(ctx, &cfg); err != nil {
		return Config{}, fmt.Errorf("environment: %w", err)
	}
	if wif := cfg.Dispatcher.KeycloakWif; *wif == (KeycloakWif{}) {
		cfg.Dispatcher.KeycloakWif = nil
	} else {
		wif.TokenLifetime = cmp.Or(wif.TokenLifetime, xtime.Minutes(defaultKeycloakWifTokenLifetimeMinutes))
	}
	cfg.Tf.CacheDir = cmp.Or(cfg.Tf.CacheDir, cfg.defaultTfCacheDir())
	if added, err := cfg.Crypto.AddImplicitPrivateKeyFile(); err != nil {
		return Config{}, err
	} else if added {
		slog.InfoContext(ctx, "using private key file", "path", crypto.ImplicitPrivateKeyFile)
	}
	return cfg, nil
}

func (cfg *Config) decode(ctx context.Context, root ast.Node) error {
	if err := envsubst.Apply(root); err != nil {
		return fmt.Errorf("environment placeholders: %w", err)
	}
	decoded := *cfg
	newErr := yaml.NodeToValue(root, &decoded, yaml.UseJSONUnmarshaler(), yaml.DisallowUnknownField())
	if newErr == nil {
		*cfg = decoded
		return nil
	}
	legacyConfig, legacyErr := legacy.Decode(root)
	if legacyErr != nil {
		return errors.Join(fmt.Errorf("new layout: %w", newErr), fmt.Errorf("legacy layout: %w", legacyErr))
	}
	return cfg.applyLegacy(ctx, legacyConfig)
}
