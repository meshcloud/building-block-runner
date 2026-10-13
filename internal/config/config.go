package config

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"uuid"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"

	"github.com/meshcloud/building-block-runner/internal/config/internal/legacy"
	"github.com/meshcloud/building-block-runner/internal/crypto"
)

// Note: Version is ignored (compile-time value in build.go via go ldflag)
type Config struct {
	Uuid uuid.UUID `yaml:"uuid" env:"UUID"`

	Api        Api           `yaml:"api" envPrefix:"API_"`
	Crypto     crypto.Crypto `yaml:"crypto" envPrefix:"CRYPTO_"`
	Dispatcher Dispatcher    `yaml:"dispatcher" envPrefix:"DISPATCHER_"`

	K8s *K8s `yaml:"k8s" envPrefix:"K8S_"`

	Tf Tf `yaml:"tf" envPrefix:"TF_"`
}

func defaultConfig() Config {
	return Config{Dispatcher: defaultDispatcher(), Tf: defaultTf()}
}

// FileFromEnv returns the path of the config file, which the run-controller read from RUNCONTROLLER_CONFIG_FILE.
func FileFromEnv() string {
	return cmp.Or(os.Getenv("RUNNER_CONFIG_FILE"), os.Getenv("RUNCONTROLLER_CONFIG_FILE"), "runner-config.yml")
}

// LoadFile loads the defaults and the environment alone when the file does not exist.
func LoadFile(path string, logger *log.Logger) (Config, error) {
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		logger.Printf("Config file %s does not exist, using defaults and environment", path)
	case err != nil:
		return Config{}, err
	}
	cfg, err := Load(data, logger)
	if err != nil {
		return Config{}, fmt.Errorf("config file %s: %w", path, err)
	}
	return cfg, nil
}

func Load(data []byte, logger *log.Logger) (Config, error) {
	file, err := parser.ParseBytes(data, 0)
	if err != nil {
		return Config{}, err
	}
	if len(file.Docs) > 1 {
		return Config{}, fmt.Errorf("config has %d yaml documents, expected one", len(file.Docs))
	}
	cfg := defaultConfig()
	if len(file.Docs) == 1 && file.Docs[0].Body != nil {
		if err := cfg.decode(file.Docs[0].Body); err != nil {
			return Config{}, err
		}
	}
	if err := applyEnvironment(&cfg, logger); err != nil {
		return Config{}, fmt.Errorf("environment: %w", err)
	}
	if added, err := cfg.Crypto.AddImplicitPrivateKeyFile(); err != nil {
		return Config{}, err
	} else if added {
		logger.Printf("Using private key file %s", crypto.ImplicitPrivateKeyFile)
	}
	return cfg, nil
}

// decode reads either the new layout or a legacy layout, so that a file cannot mix them.
func (cfg *Config) decode(root ast.Node) error {
	if err := substituteEnv(root); err != nil {
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
	return cfg.applyLegacy(legacyConfig)
}
