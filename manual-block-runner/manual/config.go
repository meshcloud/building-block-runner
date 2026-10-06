package manual

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config keeps the layout of the Kotlin runner's runner-config.yml, which customers mount as it is.
type Config struct {
	Uuid string `yaml:"uuid"`
	Api  struct {
		Url string `yaml:"url"`
	} `yaml:"api"`
	Auth struct {
		Username string `yaml:"username"`
		Password string `yaml:"password"`
		ApiKey   struct {
			ClientId     string `yaml:"clientid"`
			ClientSecret string `yaml:"clientsecret"`
		} `yaml:"apikey"`
	} `yaml:"auth"`
}

func defaultConfig() Config {
	var cfg Config
	cfg.Uuid = "d943b032-7836-4fef-a4a0-158817beecf3"
	cfg.Api.Url = "http://localhost:8301"
	cfg.Auth.Username = "bb-api"
	cfg.Auth.Password = "guest"
	return cfg
}

func LoadConfig(configFile string) (Config, error) {
	cfg := defaultConfig()
	if err := readConfigFile(configFile, &cfg); err != nil {
		return Config{}, fmt.Errorf("read %s: %w", configFile, err)
	}

	cfg.Uuid = cmp.Or(os.Getenv("RUNNER_UUID"), cfg.Uuid)
	cfg.Api.Url = cmp.Or(os.Getenv("RUNNER_API_URL"), cfg.Api.Url)
	cfg.Auth.Username = cmp.Or(os.Getenv("RUNNER_API_USERNAME"), cfg.Auth.Username)
	cfg.Auth.Password = cmp.Or(os.Getenv("RUNNER_API_PASSWORD"), cfg.Auth.Password)
	cfg.Auth.ApiKey.ClientId = cmp.Or(os.Getenv("RUNNER_API_CLIENT_ID"), cfg.Auth.ApiKey.ClientId)
	cfg.Auth.ApiKey.ClientSecret = cmp.Or(os.Getenv("RUNNER_API_CLIENT_SECRET"), cfg.Auth.ApiKey.ClientSecret)
	return cfg, nil
}

func readConfigFile(configFile string, cfg *Config) error {
	data, err := os.ReadFile(configFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return err
	}
	resolveLikeSpring(&root)
	return root.Decode(&struct {
		BlockRunner *Config `yaml:"blockrunner"`
	}{cfg})
}

var (
	springKeySeparators = strings.NewReplacer("-", "", "_", "")
	springPlaceholder   = regexp.MustCompile(`\$\{([^}:]+)(?::([^}]*))?}`)
)

// resolveLikeSpring accepts what Spring accepted in the Kotlin runner's file: any spelling of a key
// (`api-key`, `apiKey`, `api_key`), and `${ENV_VAR:default}` placeholders, as in that runner's own
// runner-config.yml.
func resolveLikeSpring(node *yaml.Node) {
	if node.Kind == yaml.MappingNode {
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			key.Value = strings.ToLower(springKeySeparators.Replace(key.Value))
		}
	}
	if node.Kind == yaml.ScalarNode {
		node.Value = springPlaceholder.ReplaceAllStringFunc(node.Value, func(placeholder string) string {
			match := springPlaceholder.FindStringSubmatch(placeholder)
			if value, ok := os.LookupEnv(match[1]); ok {
				return value
			}
			return match[2]
		})
	}
	for _, child := range node.Content {
		resolveLikeSpring(child)
	}
}
