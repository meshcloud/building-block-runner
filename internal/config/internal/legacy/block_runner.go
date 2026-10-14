package legacy

import (
	"strings"

	"github.com/goccy/go-yaml/ast"

	"github.com/meshcloud/building-block-runner/internal/crypto"
)

// BlockRunner binds the blockrunner keys of a Kotlin runner's Spring config, lowercased and without '-' and '_'.
type BlockRunner struct {
	Version string `yaml:"version"`
	Uuid    string `yaml:"uuid"`
	Api     struct {
		Url string `yaml:"url"`
	} `yaml:"api"`
	Auth struct {
		ApiKey struct {
			ClientId     string `yaml:"clientid"`
			ClientSecret string `yaml:"clientsecret"`
		} `yaml:"apikey"`
	} `yaml:"auth"`
	PrivateKey     crypto.PrivateKey     `yaml:"privatekey"`
	PrivateKeyFile crypto.PrivateKeyFile `yaml:"privatekeyfile"`
}

var springKeySeparators = strings.NewReplacer("-", "", "_", "")

type springKeyNormalizer struct{}

func (r springKeyNormalizer) Visit(node ast.Node) ast.Visitor {
	if n, ok := node.(*ast.MappingValueNode); ok {
		if key, ok := n.Key.(*ast.StringNode); ok {
			key.Value = strings.ToLower(springKeySeparators.Replace(key.Value))
		}
	}
	return r
}
