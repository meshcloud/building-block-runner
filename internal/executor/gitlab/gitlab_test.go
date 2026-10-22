package gitlab

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_InputValues_BecomeJsonTextExceptStrings(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		value any
		want  string
	}{
		{"hello", "hello"},
		{"42", "42"},
		{42.0, "42"},
		{1.5, "1.5"},
		{true, "true"},
		{[]any{"a", "c"}, `["a","c"]`},
		{map[string]any{"key": "value"}, `{"key":"value"}`},
		{nil, "null"},
	} {
		got, err := formValue(c.value)
		require.NoError(t, err)
		assert.Equal(t, c.want, got, "%#v", c.value)
	}
}
