package xtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_Duration_TakesANumberInItsDefaultUnit(t *testing.T) {
	var minutes Duration[Minute]
	require.NoError(t, minutes.UnmarshalText([]byte("5")))
	assert.Equal(t, 5*time.Minute, minutes.Duration)

	var seconds Duration[Second]
	require.NoError(t, seconds.UnmarshalText([]byte("10")))
	assert.Equal(t, 10*time.Second, seconds.Duration)

	require.NoError(t, seconds.UnmarshalText([]byte("2m")))
	assert.Equal(t, 2*time.Minute, seconds.Duration)
}
