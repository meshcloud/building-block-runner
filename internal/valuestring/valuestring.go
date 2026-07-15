// Package valuestring renders a decoded JSON value as the string form used when a
// building-block input is transported "as string" to an external CI/CD backend that only
// accepts string-valued inputs (azure devops, gitlab, github). It is the single canonical
// renderer: a value crossing that boundary must never carry language-specific formatting
// (Go's `fmt` `[a b]`/`map[k:v]`, or an empty string standing in for a null) -- the only
// portable option is inline JSON, which every consumer can parse back.
package valuestring

import "encoding/json"

// Render turns a decoded JSON value into its transport string. Scalars render as their
// literal text: a string verbatim, a json.Number's literal digits, a bool as
// "true"/"false". A JSON null renders as the literal "null", and composites (arrays/objects)
// render as compact JSON ("[\"a\",\"b\"]"/"{\"k\":\"v\"}") -- never a language-specific
// representation. A consumer that string-parses the value therefore always gets portable,
// re-parseable bytes.
//
// The input must have been decoded with json.Decoder.UseNumber() so that numeric literals
// arrive as json.Number and are rendered by their exact digits rather than float64-ized.
// The marshal-failure branch is unreachable for a value that was itself just decoded from
// JSON.
func Render(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	}
}
