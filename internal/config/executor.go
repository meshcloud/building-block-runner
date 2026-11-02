package config

import "encoding/json/v2"

func (cfg *Config) ExecutorConfig(runnerType RunnerType) (string, error) {
	if runnerType != RunnerTypeTf {
		return "", nil
	}
	encoded, err := json.Marshal(cfg.Tf)
	return string(encoded), err
}
