package config

type Metrics struct {
	Port int `yaml:"port" env:"PORT" envFallback:"PORT"`
}
