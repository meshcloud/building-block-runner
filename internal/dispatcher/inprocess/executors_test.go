package inprocess

import (
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/config/xurl"
)

func Test_NewExecutors_FailsForATypeWithoutExecutor(t *testing.T) {
	var cfg config.Config
	cfg.Dispatcher.ForTypes = config.RunnerTypes{config.RunnerTypeGitHub}

	_, err := newExecutors(cfg)

	assert.EqualError(t, err, "the in-process dispatcher cannot run github runs")
}

func Test_NewExecutors_BuildsTheConfiguredType(t *testing.T) {
	var cfg config.Config
	cfg.Api.Url = xurl.MustParse("http://localhost:8080")
	cfg.Dispatcher.ForTypes = config.RunnerTypes{config.RunnerTypeManual}

	executors, err := newExecutors(cfg)

	require.NoError(t, err)
	assert.Equal(t, []config.RunnerType{config.RunnerTypeManual}, slices.Collect(maps.Keys(executors)))
}
