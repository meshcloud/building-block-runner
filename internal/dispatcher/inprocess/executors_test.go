package inprocess

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/meshcloud/building-block-runner/internal/config"
)

func Test_NewExecutors_FailsForATypeWithoutExecutor(t *testing.T) {
	var cfg config.Config
	cfg.Dispatcher.ForTypes = config.RunnerTypes{config.RunnerTypeGitHub}

	_, err := newExecutors(cfg)

	assert.EqualError(t, err, "the in-process dispatcher cannot run github runs")
}
