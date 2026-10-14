package tf

import (
	"errors"

	"github.com/meshcloud/building-block-runner/internal/config/xtime"
)

type Config struct {
	CacheDir              string                       `json:"cacheDir"         env:"CACHE_DIR"`
	SkipHostKeyValidation bool                         `json:"insecureHostKeys" env:"INSECURE_HOST_KEYS"`
	WsTimeout             xtime.Duration[xtime.Minute] `json:"wsTimeout"        env:"WS_TIMEOUT"`
	InitTimeout           xtime.Duration[xtime.Minute] `json:"initTimeout"      env:"INIT_TIMEOUT"`
	RegistryRetries       int                          `json:"registryRetries"  env:"REGISTRY_RETRIES"`
	// StopGrace is how long tofu may take after SIGINT to save its state before it is killed.
	StopGrace xtime.Duration[xtime.Second] `json:"stopGrace" env:"STOP_GRACE"`
}

func Default() Config {
	return Config{
		WsTimeout:       xtime.Minutes(5),
		InitTimeout:     xtime.Minutes(3),
		RegistryRetries: 3,
		StopGrace:       xtime.Seconds(60),
	}
}

func (cfg Config) Validate() error {
	var errs []error
	if cfg.CacheDir == "" {
		errs = append(errs, errors.New("tf.cacheDir is required"))
	}
	if cfg.WsTimeout.Duration <= 0 {
		errs = append(errs, errors.New("tf.wsTimeout must be positive"))
	}
	if cfg.InitTimeout.Duration <= 0 {
		errs = append(errs, errors.New("tf.initTimeout must be positive"))
	}
	if cfg.RegistryRetries < 0 {
		errs = append(errs, errors.New("tf.registryRetries must not be negative"))
	}
	if cfg.StopGrace.Duration <= 0 {
		errs = append(errs, errors.New("tf.stopGrace must be positive"))
	}
	return errors.Join(errs...)
}
