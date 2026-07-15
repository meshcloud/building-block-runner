package valuestring

import (
	"encoding/json"
	"testing"
)

// Test_Render covers the shared value-stringification: scalar rows are byte-identical to
// the Kotlin `value.toString()` baseline; composite rows assert the compact-JSON delta
// instead of Java's toString ("[a, b]"/"{k=v}").
func Test_Render(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"string verbatim", "hello", "hello"},
		{"integer literal", json.Number("4"), "4"},
		{"large integer preserves digits", json.Number("123456789012345678901234"), "123456789012345678901234"},
		{"bool true", true, "true"},
		{"bool false", false, "false"},
		{"null", nil, "null"},
		{"array as compact JSON (flagged delta vs Java toString)", []any{"a", "b"}, `["a","b"]`},
		{"object as compact JSON (flagged delta vs Java toString)", map[string]any{"k": "v"}, `{"k":"v"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Render(c.in); got != c.want {
				t.Errorf("Render(%#v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
