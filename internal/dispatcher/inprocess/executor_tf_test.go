//go:build tf && !manual

package inprocess

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/meshcloud/building-block-runner/internal/config"
)

func Test_DefaultForTypes_IsTheOnlyLinkedExecutor(t *testing.T) {
	assert.Equal(t, config.RunnerTypes{config.RunnerTypeTf}, defaultForTypes())
}
