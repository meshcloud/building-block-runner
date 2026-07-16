package github

import (
	"net/http"
	"strings"
	"testing"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/report"
)

// singleLinePem returns a valid PKCS#1 PEM with no internal newlines (parseAppPem's tolerant
// path accepts it) so it embeds cleanly in a JSON string fixture.
func singleLinePem(t *testing.T) string {
	t.Helper()
	_, pemStr := testKey(t)
	body := pemStr
	body = strings.ReplaceAll(body, "-----BEGIN RSA PRIVATE KEY-----", "")
	body = strings.ReplaceAll(body, "-----END RSA PRIVATE KEY-----", "")
	body = removeAllWhitespace(body)
	return "-----BEGIN RSA PRIVATE KEY-----" + body + "-----END RSA PRIVATE KEY-----"
}

// runFixture builds claim-shaped run JSON for scenarios with the knobs the scenarios vary.
type runFixture struct {
	baseURL     string
	behavior    string // default APPLY
	inputsJSON  string // default []
	appPem      string // default "not-a-pem"
	async       bool
	omit        bool
	destroyNull bool
	implType    string // default GITHUB_WORKFLOW
}

func (f runFixture) json() string {
	behavior := f.behavior
	if behavior == "" {
		behavior = "APPLY"
	}
	inputs := f.inputsJSON
	if inputs == "" {
		inputs = "[]"
	}
	implType := f.implType
	if implType == "" {
		implType = "GITHUB_WORKFLOW"
	}
	destroy := `"destroyWorkflow":"destroy.yml",`
	if f.destroyNull {
		destroy = `"destroyWorkflow":null,`
	}
	return `{
      "kind":"meshBuildingBlockRun","apiVersion":"v1",
      "metadata":{"uuid":"run-1"},
      "spec":{
        "runNumber":1,
        "buildingBlock":{"uuid":"bb-1","spec":{
          "displayName":"name","workspaceIdentifier":"ws","inputs":` + inputs + `,"parentBuildingBlocks":[]}},
        "buildingBlockDefinition":{"uuid":"def-1","spec":{
          "workspaceIdentifier":"defws","version":1,
          "implementation":{"type":"` + implType + `","githubBaseUrl":"` + f.baseURL + `",
            "owner":"owner","appId":"123","appPem":"` + f.appPem + `","repository":"repo",
            "branch":"main","applyWorkflow":"apply.yml",` + destroy +
		`"async":` + boolStr(f.async) + `,"omitRunObjectInput":` + boolStr(f.omit) + `}}},
        "behavior":"` + behavior + `","runToken":"rt"},
      "status":"IN_PROGRESS",
      "_links":{"self":{"href":"https://meshstack.example.com/run/run-1"}}}`
}

// claim builds a ClaimedRun from the fixture JSON.
func (f runFixture) claim(t *testing.T) dispatch.ClaimedRun {
	t.Helper()
	raw := f.json()
	d := parseRun(t, raw)
	return dispatch.ClaimedRun{
		Id:      dispatch.RunId(d.Metadata.Uuid),
		Type:    meshapi.RunnerTypeGitHubWorkflow,
		Run:     d,
		RawJson: encodeRawJSON(raw),
	}
}

// newTestHandler wires a Handler against the stub GitHub with a shared fakeReporter
// (fixtures embed plaintext PEMs/inputs -- decryption happens at the dispatch boundary,
// not in the handler) and the given clock.
func newTestHandler(t *testing.T, stub *githubStub, clock Clock) (Handler, *fakeReporter) {
	t.Helper()
	return newTestHandlerWithHTTP(t, clock, stub.server.Client())
}

// newTestHandlerWithHTTP is newTestHandler with an explicit HTTP client, for redirect/retry
// scenarios that must swap the transport instead of the stub's plain client.
func newTestHandlerWithHTTP(t *testing.T, clock Clock, hc *http.Client) (Handler, *fakeReporter) {
	t.Helper()
	rep := &fakeReporter{}
	h := NewHandler(Config{BaseConfig: config.BaseConfig{Uuid: "runner"}}, HandlerDeps{
		Reporters: func(dispatch.ClaimedRun) report.Reporter { return rep },
		HTTP:      hc,
		Clock:     clock,
	})
	return h, rep
}

// stepByName returns the step with the given id from a RunStatus, or a zero StepStatus.
func stepByName(s report.RunStatus, name string) report.StepStatus {
	for _, st := range s.Steps {
		if st.Name == name {
			return st
		}
	}
	return report.StepStatus{}
}

func derefOr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
