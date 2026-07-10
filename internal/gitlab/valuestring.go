package gitlab

import "encoding/json"

// valueString renders a decoded JSON value the way it becomes a GitLab multipart form
// field value, replacing Kotlin's `value.toString()` (GitLabClient.kt:131,141). Scalars
// are byte-identical to the Kotlin baseline (a string verbatim, a json.Number's literal
// digits, a bool as "true"/"false"); composites (arrays/objects) render as compact JSON
// instead of Java's toString ("[a, b]"/"{k=v}") -- a deliberate, flagged delta (plan 06B
// §16.4/G-P8): the pins assert JSON, and a pipeline parsing the old Java-toString form
// gets strictly better (parseable) bytes instead. A JSON null -- unrepresentable in
// Kotlin's `value: Any` (the whole claim would fail to parse there, flag §16.5) -- renders
// as the literal string "null" and the run proceeds.
func valueString(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case json.Number:
		return t.String()
	default:
		// Arrays/objects (decoded as []any / map[string]any by encoding/json). A marshal
		// failure cannot occur for values that were themselves just decoded from JSON.
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	}
}
