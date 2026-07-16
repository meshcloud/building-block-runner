package meshapi

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func handoverRunJSONFixture(t *testing.T, implJSON string) []byte {
	t.Helper()
	doc := `{
		"apiVersion": "v1", "kind": "MeshBuildingBlockRun",
		"metadata": {"uuid": "run-1"},
		"spec": {
			"runToken": "the-run-token",
			"behavior": "APPLY",
			"buildingBlockDefinition": {
				"spec": {"implementation": ` + implJSON + `}
			},
			"buildingBlock": {"spec": {"inputs": [{"key":"a","value":"visible","type":"STRING","isSensitive":false}]}}
		},
		"_links": {"self": {"href": "http://x/self"}}
	}`
	return []byte(doc)
}

func decodeDoc(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	require.NoError(t, d.Decode(&doc))
	return doc
}

func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	require.True(t, ok)
	return m
}

func implOf(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	spec := asMap(t, doc["spec"])
	def := asMap(t, spec["buildingBlockDefinition"])
	defSpec := asMap(t, def["spec"])
	return asMap(t, defSpec["implementation"])
}

// TestSanitizeRunObjectForHandover_ReducedToType pins the core behavior: whatever the
// implementation object was, the sanitized doc's implementation is exactly {"type": ...}.
func TestSanitizeRunObjectForHandover_ReducedToType(t *testing.T) {
	raw := handoverRunJSONFixture(t, `{"type":"GITLAB_CICD","gitlabBaseUrl":"https://gitlab.example.com","pipelineTriggerToken":"still-ciphertext"}`)

	out, err := SanitizeRunObjectForHandover(raw)
	require.NoError(t, err)

	impl := implOf(t, decodeDoc(t, out))
	require.Equal(t, map[string]any{"type": "GITLAB_CICD"}, impl)
}

// TestSanitizeRunObjectForHandover_PerTypeSecretsStripped pins each secret field named in the
// task by type: appPem (github), pipelineTriggerToken (gitlab), personalAccessToken (azdevops),
// sshPrivateKey (terraform) never survive into the sanitized output.
func TestSanitizeRunObjectForHandover_PerTypeSecretsStripped(t *testing.T) {
	cases := []struct {
		name      string
		implJSON  string
		secretVal string
	}{
		{
			name:      "github appPem",
			implJSON:  `{"type":"GITHUB_WORKFLOW","owner":"o","appPem":"-----BEGIN KEY-----secret"}`,
			secretVal: "-----BEGIN KEY-----secret",
		},
		{
			name:      "gitlab pipelineTriggerToken",
			implJSON:  `{"type":"GITLAB_CICD","projectId":"1","pipelineTriggerToken":"trigger-secret"}`,
			secretVal: "trigger-secret",
		},
		{
			name:      "azdevops personalAccessToken",
			implJSON:  `{"type":"AZURE_DEVOPS","organization":"o","personalAccessToken":"pat-secret"}`,
			secretVal: "pat-secret",
		},
		{
			name:      "terraform sshPrivateKey",
			implJSON:  `{"type":"TERRAFORM","repositoryUrl":"git@x","sshPrivateKey":"ssh-secret"}`,
			secretVal: "ssh-secret",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := handoverRunJSONFixture(t, tc.implJSON)
			out, err := SanitizeRunObjectForHandover(raw)
			require.NoError(t, err)
			require.NotContains(t, string(out), tc.secretVal)
		})
	}
}

// TestSanitizeRunObjectForHandover_OtherFieldsPreserved proves inputs, _links, runToken, and
// behavior pass through byte-wise unchanged.
func TestSanitizeRunObjectForHandover_OtherFieldsPreserved(t *testing.T) {
	raw := handoverRunJSONFixture(t, `{"type":"MANUAL"}`)
	out, err := SanitizeRunObjectForHandover(raw)
	require.NoError(t, err)

	doc := decodeDoc(t, out)
	spec := asMap(t, doc["spec"])
	require.Equal(t, "the-run-token", spec["runToken"])
	require.Equal(t, "APPLY", spec["behavior"])

	links := asMap(t, doc["_links"])
	self := asMap(t, links["self"])
	require.Equal(t, "http://x/self", self["href"])

	bb := asMap(t, spec["buildingBlock"])
	bbSpec := asMap(t, bb["spec"])
	inputs, ok := bbSpec["inputs"].([]any)
	require.True(t, ok)
	require.Len(t, inputs, 1)
	in := asMap(t, inputs[0])
	require.Equal(t, "visible", in["value"])
}

// TestSanitizeRunObjectForHandover_EmptyOrAbsentImpl documents the chosen behavior for a
// missing/malformed implementation node: pass the doc through unchanged rather than failing
// a handover that doesn't need one.
func TestSanitizeRunObjectForHandover_EmptyOrAbsentImpl(t *testing.T) {
	for _, doc := range []string{
		`{}`,
		`{"spec":{}}`,
		`{"spec":{"buildingBlockDefinition":{}}}`,
		`{"spec":{"buildingBlockDefinition":{"spec":{}}}}`,
		`{"spec":{"buildingBlockDefinition":{"spec":{"implementation":null}}}}`,
		`{"spec":{"buildingBlockDefinition":{"spec":{"implementation":"not-an-object"}}}}`,
	} {
		out, err := SanitizeRunObjectForHandover([]byte(doc))
		require.NoError(t, err, doc)
		require.JSONEq(t, doc, string(out), doc)
	}
}

// TestSanitizeRunObjectForHandover_MalformedJSON propagates a JSON parse failure.
func TestSanitizeRunObjectForHandover_MalformedJSON(t *testing.T) {
	_, err := SanitizeRunObjectForHandover([]byte("{not json"))
	require.Error(t, err)
}

// TestSensitiveInputKeys_Sorted pins the sorted, sensitive-only selection SensitiveInputKeys
// gives callers for their single WARN log line.
func TestSensitiveInputKeys_Sorted(t *testing.T) {
	inputs := []BuildingBlockInputSpecDTO{
		{Key: "zeta", IsSensitive: true},
		{Key: "plain", IsSensitive: false},
		{Key: "alpha", IsSensitive: true},
		{Key: "mid", IsSensitive: true},
	}
	require.Equal(t, []string{"alpha", "mid", "zeta"}, SensitiveInputKeys(inputs))
}

// TestSensitiveInputKeys_None covers the no-sensitive-inputs case: an empty, non-nil slice.
func TestSensitiveInputKeys_None(t *testing.T) {
	inputs := []BuildingBlockInputSpecDTO{{Key: "a", IsSensitive: false}}
	got := SensitiveInputKeys(inputs)
	require.Empty(t, got)
}
