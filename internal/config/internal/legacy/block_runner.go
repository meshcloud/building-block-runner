package legacy

import (
	"strings"

	"github.com/goccy/go-yaml/ast"

	"github.com/meshcloud/building-block-runner/internal/crypto"
)

// BlockRunner is the blockrunner block of the Kotlin runners' Spring config, with its keys lowercased
// and stripped of '-' and '_'.
type BlockRunner struct {
	Version string `yaml:"version"`
	Uuid    string `yaml:"uuid"`
	Api     struct {
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
