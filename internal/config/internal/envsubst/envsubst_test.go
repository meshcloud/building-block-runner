package envsubst

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_EnvSubstitution_ExpandsSpringPlaceholders(t *testing.T) {
	t.Setenv("SET", "value")
	t.Setenv("EMPTY", "")
	t.Setenv("RUNNER_APIURL", "relaxed")

	t.Run("expands placeholders", func(t *testing.T) {
		for input, expected := range map[string]string{
			"plain":                   "plain",
			"${SET}":                  "value",
			"a-${SET}-b":              "a-value-b",
			"${SET}${SET}":            "valuevalue",
			"${UNSET:default}":        "default",
			"${EMPTY:default}":        "default",
			"${UNSET:-1}":             "-1",
			"${SET:default}":          "value",
			"${UNSET:}":               "",
			"${UNSET:http://x:8080}":  "http://x:8080",
			"${UNSET:${SET}}":         "value",
			"${UNSET:${UNSET2:deep}}": "deep",
			"${runner.api-url}":       "relaxed",
			`\${SET}`:                 "${SET}",
			"pa$$word $SET $(K8S)":    "pa$$word $SET $(K8S)",
		} {
			actual, err := expandPlaceholders(input)
			if assert.NoError(t, err, input) {
				assert.Equal(t, expected, actual, input)
			}
		}
	})

	t.Run("fails on an unset or broken placeholder", func(t *testing.T) {
		for input, expectedErr := range map[string]string{
			"${UNSET}":        "environment variable UNSET of placeholder ${UNSET} is not set or empty",
			"${EMPTY}":        "environment variable EMPTY of placeholder ${EMPTY} is not set or empty",
			"${UNSET:${NO}}":  "environment variable NO of placeholder ${NO} is not set or empty",
			"${SET":           "placeholder ${SET has no closing brace",
			"${:default}":     "placeholder ${:default} has no name",
			"a ${UNSET:${X}b": "has no closing brace",
		} {
			_, err := expandPlaceholders(input)
			assert.ErrorContains(t, err, expectedErr, input)
		}
	})
}
