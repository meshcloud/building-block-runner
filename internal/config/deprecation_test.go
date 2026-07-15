package config

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestWarnDeprecated_UniformWording pins the one shared message shape every deprecation
// warning in this repo now goes through -- old, new and a
// pointer to the documented alias inventory + removal timeline.
func TestWarnDeprecated_UniformWording(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	WarnDeprecated(log, "OLD_VAR", "NEW_VAR")

	out := buf.String()
	assert.Contains(t, out, "deprecated: OLD_VAR is deprecated, use NEW_VAR instead")
	assert.Contains(t, out, DeprecationDoc)
}
