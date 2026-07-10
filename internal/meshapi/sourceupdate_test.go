package meshapi

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSourceUpdateDTO_OmitemptyIsAbsentNotNull pins the null ≡ absent equivalence (§16.4):
// unset optional fields are omitted from the JSON, not serialized as explicit null, so the
// lean wire body carries only what changed (§7.4).
func TestSourceUpdateDTO_OmitemptyIsAbsentNotNull(t *testing.T) {
	// A status-only update: no steps.
	body, err := json.Marshal(SourceUpdateDTO{Status: "IN_PROGRESS"})
	require.NoError(t, err)
	require.JSONEq(t, `{"status":"IN_PROGRESS"}`, string(body))

	// A step carrying only id+status: display/message/outputs fields are absent.
	body, err = json.Marshal(SourceUpdateDTO{
		Status: "SUCCEEDED",
		Steps:  []StepUpdateDTO{{Id: "manual", Status: "SUCCEEDED"}},
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"status":"SUCCEEDED","steps":[{"id":"manual","status":"SUCCEEDED"}]}`, string(body))
}
