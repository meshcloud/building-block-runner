package gitlab

import (
	"encoding/json"
	"testing"
)

// Test_ValueString ports the G-P8 stringification pin: scalar rows are byte-identical to
// the Kotlin `value.toString()` baseline; composite rows assert the flagged compact-JSON
// delta (§16.4) instead of Java's toString ("[a, b]"/"{k=v}").
func Test_ValueString(t *testing.T) {
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
			if got := valueString(c.in); got != c.want {
				t.Errorf("valueString(%#v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
