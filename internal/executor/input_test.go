package executor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_InputText_KeepsStringsAndRendersOtherValuesAsJson(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		value any
		want  string
	}{
		{nil, ""},
		{"hello", "hello"},
		{"42", "42"},
		{`say "hi"`, `say "hi"`},
		{42.0, "42"},
		{1.5, "1.5"},
		{int64(12345678901234567), "12345678901234567"},
		{true, "true"},
		{false, "false"},
		{[]any{"a", "c"}, `["a","c"]`},
		{[]any{}, `[]`},
		{map[string]any{"key": "value"}, `{"key":"value"}`},
	} {
		got, err := InputText(c.value)
		require.NoError(t, err)
		assert.Equal(t, c.want, got, "%#v", c.value)
	}
}
