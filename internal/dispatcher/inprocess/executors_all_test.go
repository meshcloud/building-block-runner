//go:build !tf && !manual

package inprocess

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_DefaultForTypes_IsEmptyWithSeveralExecutors(t *testing.T) {
	assert.Empty(t, defaultForTypes())
}
