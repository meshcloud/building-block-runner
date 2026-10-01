package meshapi

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPredecessorArtifactHref(t *testing.T) {
	tests := []struct {
		name  string
		links string
		want  string
	}{
		{
			name:  "reads the artifact rel",
			links: `{"artifact": {"href": "https://meshstack/runs/r/artifact"}}`,
			want:  "https://meshstack/runs/r/artifact",
		},
		{
			name:  "falls back to the planArtifact rel of meshStack versions before the rename",
			links: `{"planArtifact": {"href": "https://meshstack/runs/r/plan-artifact"}}`,
			want:  "https://meshstack/runs/r/plan-artifact",
		},
		{
			name: "prefers the artifact rel when meshStack emits both",
			links: `{"artifact": {"href": "https://meshstack/runs/r/artifact"},
				"planArtifact": {"href": "https://meshstack/runs/r/plan-artifact"}}`,
			want: "https://meshstack/runs/r/artifact",
		},
		{
			name:  "is empty when the run has no predecessor artifact",
			links: `{}`,
			want:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var links LinksDTO
			require.NoError(t, json.Unmarshal([]byte(tc.links), &links))

			assert.Equal(t, tc.want, links.PredecessorArtifactHref())
		})
	}
}
