package legacy

import (
	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
)

type Config struct {
	RunController `yaml:",inline"`
	TfRunner      `yaml:",inline"`
	Spring        `yaml:",inline"`

	Api         Api          `yaml:"api"`
	BlockRunner *BlockRunner `yaml:"-"`
}

// Spring holds the top-level keys of a Spring config, of which only blockrunner configured the runner.
type Spring struct {
	BlockRunnerKeys any `yaml:"blockrunner"`
	Server          any `yaml:"server"`
	Logging         any `yaml:"logging"`
	Spring          any `yaml:"spring"`
	Management      any `yaml:"management"`
}

type Api struct {
	Url          string `yaml:"url"`
	ClientId     string `yaml:"clientId"`
	ClientSecret string `yaml:"clientSecret"`
}

// Decode rewrites the keys below blockrunner in place to bind them like Spring did.
func Decode(root ast.Node) (cfg Config, err error) {
	if err = yaml.NodeToValue(root, &cfg, yaml.DisallowUnknownField()); err != nil {
		return
	}
	blockRunner, err := (&yaml.PathBuilder{}).Root().Child("blockrunner").Build().FilterNode(root)
	if err != nil || blockRunner == nil {
		return
	}
	ast.Walk(springKeyNormalizer{}, blockRunner)
	cfg.BlockRunner = &BlockRunner{}
	err = yaml.NodeToValue(blockRunner, cfg.BlockRunner, yaml.DisallowUnknownField())
	return
}
