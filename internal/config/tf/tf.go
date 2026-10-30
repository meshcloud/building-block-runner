package tf

import (
	"errors"

	"github.com/meshcloud/building-block-runner/v2/internal/config/xtime"
)

type Config struct {
	CacheDir              string                       `json:"cacheDir"         env:"CACHE_DIR"`
	SkipHostKeyValidation bool                         `json:"insecureHostKeys" env:"INSECURE_HOST_KEYS"`
	InitTimeout           xtime.Duration[xtime.Minute] `json:"initTimeout"      env:"INIT_TIMEOUT"`
	InitRetries           int                          `json:"initRetries"      env:"INIT_RETRIES"`
	AbortGrace            xtime.Duration[xtime.Second] `json:"abortGrace" env:"ABORT_GRACE"`
}

func Default() Config {
	return Config{
		InitTimeout: xtime.Minutes(5),
		InitRetries: 3,
		AbortGrace:  xtime.Seconds(60),
	}
}

func (cfg Config) Validate() error {
	var errs []error
	if cfg.CacheDir == "" {
		errs = append(errs, errors.New("tf.cacheDir is required"))
	}
	if cfg.InitTimeout.Duration <= 0 {
		errs = append(errs, errors.New("tf.initTimeout must be positive"))
	}
	if cfg.InitRetries < 0 {
		errs = append(errs, errors.New("tf.initRetries must not be negative"))
	}
	if cfg.AbortGrace.Duration <= 0 {
		errs = append(errs, errors.New("tf.abortGrace must be positive"))
	}
	return errors.Join(errs...)
}
