package azuredevops

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_InputValues_BecomeTemplateParameterStrings(t *testing.T) {
	t.Parallel()
	for value, want := range map[any]string{
		nil:                      "",
		"hello":                  "hello",
		true:                     "true",
		int64(12345678901234567): "12345678901234567",
	} {
		got, err := parameterValue(value)
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
	got, err := parameterValue([]any{"a", "b"})
	require.NoError(t, err)
	assert.JSONEq(t, `["a","b"]`, got)
}
