package github

import (
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/meshcloud/building-block-runner/internal/meshapi"
)

// runJSON builds a claim-shaped run JSON with the given inputs JSON, omitRunObjectInput and
// self href. The implementation carries realistic secret-bearing fields so the G-P10 leak
// test is meaningful.
func runJSON(inputsJSON string, omit bool, selfHref string) string {
	return `{
      "kind":"meshBuildingBlockRun","apiVersion":"v1",
      "metadata":{"uuid":"test"},
      "spec":{
        "runNumber":1,
        "buildingBlock":{"uuid":"test","spec":{
          "displayName":"name","workspaceIdentifier":"workspace","projectIdentifier":"project",
          "fullPlatformIdentifier":"platform","inputs":` + inputsJSON + `,"parentBuildingBlocks":[]}},
        "buildingBlockDefinition":{"uuid":"test","spec":{
          "workspaceIdentifier":"test-workspace","version":1,
          "implementation":{"type":"GITHUB_WORKFLOW","githubBaseUrl":"https://api.github.com",
            "owner":"secret-owner","appId":"999","appPem":"SUPER_SECRET_PEM","repository":"secret-repo",
            "branch":"main","applyWorkflow":"apply.yml","destroyWorkflow":"destroy.yml","async":true,
            "omitRunObjectInput":` + boolStr(omit) + `}}},
        "behavior":"APPLY","runToken":"test"},
      "status":"IN_PROGRESS",
      "_links":{"self":{"href":"` + selfHref + `"}}}`
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func parseRun(t *testing.T, raw string) *meshapi.RunDetailsDTO {
	t.Helper()
	d, err := meshapi.ParseRunDetails([]byte(raw))
	if err != nil {
		t.Fatalf("parsing run: %v", err)
	}
	return d
}

func b64OfRun(raw string) string {
	return encodeRawJSON(raw)
}

// Test_ModeA_Parity_And_Leak pins the Mode-A payload: parsed-JSON parity with the Kotlin
// fixture field set + values (typed inputs preserved), and G-P10 (no impl secrets leak; the
// implementation is stripped to {type}).
func Test_ModeA_Parity_And_Leak(t *testing.T) {
	inputs := `[
      {"key":"test1","value":"1","type":"STRING","isSensitive":false,"isEnvironment":true},
      {"key":"test2","value":"2","type":"STRING","isSensitive":true,"isEnvironment":true},
      {"key":"test3","value":"3","type":"FILE","isSensitive":true,"isEnvironment":true},
      {"key":"test4","value":4,"type":"INTEGER","isSensitive":true,"isEnvironment":true}]`
	raw := runJSON(inputs, false, "https://meshstack.example.com/api/meshobjects/meshbuildingblockruns/test")
	details := parseRun(t, raw)

	// NoOp decryptor ⇒ values unchanged, matching the Kotlin byte fixture ("1","2","3",4).
	decoded, err := decodeAndDecryptInputs(b64OfRun(raw), details, NoOpDecryptor{}, slog.Default())
	if err != nil {
		t.Fatalf("decode/decrypt inputs: %v", err)
	}

	payload, err := modeARunObject(decryptedRun{details: details, inputs: decoded})
	if err != nil {
		t.Fatalf("modeARunObject: %v", err)
	}
	got := b64decode(t, payload)

	// G-P10: no impl secret substrings appear anywhere in the payload.
	for _, secret := range []string{"SUPER_SECRET_PEM", "secret-owner", "secret-repo", "appPem", "githubBaseUrl", "applyWorkflow", "999"} {
		if strings.Contains(string(got), secret) {
			t.Errorf("payload leaks %q:\n%s", secret, string(got))
		}
	}

	// Parsed-JSON parity: assert the field set + values (null ≡ absent, §16.4).
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	assertEq(t, "kind", m["kind"], "meshBuildingBlockRun")
	assertEq(t, "apiVersion", m["apiVersion"], "v1")

	spec := asMap(t, m["spec"])
	assertEq(t, "behavior", spec["behavior"], "APPLY")
	assertEq(t, "runToken", spec["runToken"], "test")

	defn := asMap(t, spec["buildingBlockDefinition"])
	defnSpec := asMap(t, defn["spec"])
	impl := asMap(t, defnSpec["implementation"])
	if len(impl) != 1 || impl["type"] != "GITHUB_WORKFLOW" {
		t.Errorf("implementation = %v; want only {type:GITHUB_WORKFLOW}", impl)
	}

	// _links.self.href included (legacy callback), runToken included — both intentional.
	links := asMap(t, m["_links"])
	self := asMap(t, links["self"])
	assertEq(t, "self.href", self["href"], "https://meshstack.example.com/api/meshobjects/meshbuildingblockruns/test")

	// typed inputs preserved: test4 stays a JSON number.
	bbSpec := asMap(t, asMap(t, spec["buildingBlock"])["spec"])
	inSlice := asSlice(t, bbSpec["inputs"])
	if len(inSlice) != 4 {
		t.Fatalf("inputs len = %d; want 4", len(inSlice))
	}
	test4 := asMap(t, inSlice[3])
	// Re-parsed here without UseNumber, so a preserved JSON number reads back as float64
	// (the point is it is a NUMBER, not a quoted string).
	_ = asFloat(t, test4["value"])
}

// Test_ModeA_NumberFidelity pins D8: a large integer input survives as a number, not
// float-reformatted, through the base64 payload.
func Test_ModeA_NumberFidelity(t *testing.T) {
	inputs := `[{"key":"big","value":9007199254740993,"type":"INTEGER","isSensitive":false,"isEnvironment":false}]`
	raw := runJSON(inputs, false, "https://x/self")
	details := parseRun(t, raw)
	decoded, err := decodeAndDecryptInputs(b64OfRun(raw), details, NoOpDecryptor{}, slog.Default())
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	payload, err := modeARunObject(decryptedRun{details: details, inputs: decoded})
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	if !strings.Contains(string(b64decode(t, payload)), `"value":9007199254740993`) {
		t.Errorf("large integer not preserved verbatim:\n%s", string(b64decode(t, payload)))
	}
}

// Test_DispatchInputs pins the Mode-B table (§2.4): URL always, tokens iff present, endpoint
// iff api-token present AND endpoint present.
func Test_DispatchInputs(t *testing.T) {
	self := "https://meshstack.example.com/run/x"
	impl := meshapi.GithubImplementation{OmitRunObjectInput: true}

	tests := []struct {
		name   string
		inputs []runInput
		want   map[string]string
	}{
		{
			"url-only",
			nil,
			map[string]string{inputKeyRunUrl: self},
		},
		{
			"api-and-run-token",
			[]runInput{{Key: inputKeyApiToken, Value: "api"}, {Key: inputKeyRunToken, Value: "run"}},
			map[string]string{inputKeyRunUrl: self, inputKeyApiToken: "api", inputKeyRunToken: "run"},
		},
		{
			"endpoint-with-api-token",
			[]runInput{{Key: inputKeyApiToken, Value: "api"}, {Key: inputKeyEndpoint, Value: "https://ep"}},
			map[string]string{inputKeyRunUrl: self, inputKeyApiToken: "api", inputKeyEndpoint: "https://ep"},
		},
		{
			"endpoint-without-api-token-is-dropped",
			[]runInput{{Key: inputKeyEndpoint, Value: "https://ep"}},
			map[string]string{inputKeyRunUrl: self},
		},
		{
			"run-token-only",
			[]runInput{{Key: inputKeyRunToken, Value: "run"}},
			map[string]string{inputKeyRunUrl: self, inputKeyRunToken: "run"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			details := &meshapi.RunDetailsDTO{}
			details.Links.Self.Href = self
			got, err := dispatchInputs(decryptedRun{details: details, inputs: tc.inputs}, impl)
			if err != nil {
				t.Fatalf("dispatchInputs: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v; want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("key %q = %q; want %q", k, got[k], v)
				}
			}
		})
	}
}

// Test_ModeB_MissingSelfLink pins the "No self link found" error (⇒ FAILED generic).
func Test_ModeB_MissingSelfLink(t *testing.T) {
	details := &meshapi.RunDetailsDTO{}
	details.Metadata.Uuid = "abc"
	_, err := dispatchInputs(decryptedRun{details: details}, meshapi.GithubImplementation{OmitRunObjectInput: true})
	if err == nil || !strings.Contains(err.Error(), "No self link found for building block run abc") {
		t.Fatalf("expected missing-self-link error, got %v", err)
	}
}

// Test_DecryptInputs_SensitiveTypes pins umbrella §4 row 8: sensitive STRING/CODE/FILE are
// decrypted; a sensitive non-decryptable type is left as-is; non-sensitive untouched.
func Test_DecryptInputs_SensitiveTypes(t *testing.T) {
	inputs := `[
      {"key":"s","value":"c1","type":"STRING","isSensitive":true,"isEnvironment":false},
      {"key":"code","value":"c2","type":"CODE","isSensitive":true,"isEnvironment":false},
      {"key":"file","value":"c3","type":"FILE","isSensitive":true,"isEnvironment":false},
      {"key":"num","value":7,"type":"INTEGER","isSensitive":true,"isEnvironment":false},
      {"key":"plain","value":"pt","type":"STRING","isSensitive":false,"isEnvironment":false}]`
	raw := runJSON(inputs, true, "https://x/self")
	details := parseRun(t, raw)

	out, err := decodeAndDecryptInputs(b64OfRun(raw), details, fakeDecryptor{}, slog.Default())
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	byKey := map[string]runInput{}
	for _, in := range out {
		byKey[in.Key] = in
	}
	assertEq(t, "s", byKey["s"].Value, "dec:c1")
	assertEq(t, "code", byKey["code"].Value, "dec:c2")
	assertEq(t, "file", byKey["file"].Value, "dec:c3")
	// sensitive INTEGER left as-is (json.Number).
	if got := valueToString(byKey["num"].Value); got != "7" {
		t.Errorf("num left as-is = %q; want 7", got)
	}
	assertEq(t, "plain", byKey["plain"].Value, "pt")
}

type fakeDecryptor struct{}

func (fakeDecryptor) Decrypt(c string) (string, error) { return "dec:" + c, nil }

func assertEq(t *testing.T, field string, got, want any) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %#v; want %#v", field, got, want)
	}
}
