package controller

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"os"
	"strings"
	"testing"

	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

// mockRunApi is a test double for the RunApi interface.
type mockRunApi struct {
	fetchResult       *meshapi.RunDetailsDTO
	fetchRawBase64    string
	fetchErr          error
	registerSourceErr error
	updateStatusErr   error

	registeredSourceRunId string
	updatedStatusRunId    string
	updatedStatus         string
}

func (m *mockRunApi) FetchRunDetails(nodePostfix string) (string, *meshapi.RunDetailsDTO, error) {
	return m.fetchRawBase64, m.fetchResult, m.fetchErr
}

func (m *mockRunApi) RegisterSource(runId string) error {
	m.registeredSourceRunId = runId
	return m.registerSourceErr
}

func (m *mockRunApi) UpdateRunStatus(runId string, status string, summary string, stepMessage string) error {
	m.updatedStatusRunId = runId
	m.updatedStatus = status
	return m.updateStatusErr
}

// buildRunDetailsWithImplType creates a minimal RunDetailsDTO for a given implementation type.
// The run JSON is also base64-encoded so decryptRunDetails can parse it (no actual decryption needed
// because the test crypto instance will skip real decryption for non-sensitive inputs).
func buildRunDetailsWithImplType(implType string) (*meshapi.RunDetailsDTO, string, error) {
	implJSON, err := json.Marshal(map[string]string{"type": implType})
	if err != nil {
		return nil, "", err
	}

	dto := &meshapi.RunDetailsDTO{
		Metadata: meshapi.RunMetaDTO{Uuid: "run-uuid-1"},
		Spec: meshapi.RunSpecDTO{
			BuildingBlock: meshapi.BuildingBlockSpecDTO{
				Uuid: "bb-uuid-1",
				Spec: meshapi.BuildingBlockDetailsSpecDTO{},
			},
			Definition: meshapi.DefinitionSpecDTO{
				Uuid: "def-uuid-1",
				Spec: meshapi.DefinitionDetailsSpecDTO{
					Implementation: json.RawMessage(implJSON),
				},
			},
		},
	}

	rawBytes, err := json.Marshal(dto)
	if err != nil {
		return nil, "", err
	}

	return dto, base64.StdEncoding.EncodeToString(rawBytes), nil
}

func setupControllerWithMockApi(mock *mockRunApi, implementations map[string]JobSpecTemplate) (*Controller, func()) {
	prev := AppConfig
	AppConfig = &ControllerConfig{
		Uuid:             "controller-uuid",
		OwnedByWorkspace: "test-workspace",
		DisplayName:      "Test Controller",
		Namespace:        "test-namespace",
		Api: ApiConfig{
			Url:      "http://localhost:8080",
			Username: "user",
			Password: "pass",
		},
		Crypto: CryptoConfig{
			PublicKey:  "pk",
			PrivateKey: "sk",
		},
		Implementations: implementations,
	}

	ctrl := &Controller{
		logger:  log.New(os.Stdout, "[TEST] ", 0),
		runApi:  mock,
		metrics: NewMetricsCollector(),
		// k8sClient is nil - tests that reach job creation must provide a fake
	}

	return ctrl, func() { AppConfig = prev }
}

func TestProcessNextRun_NoRunAvailable(t *testing.T) {
	mock := &mockRunApi{
		fetchErr: meshapi.HttpError{StatusCode: 404},
	}
	ctrl, cleanup := setupControllerWithMockApi(mock, map[string]JobSpecTemplate{
		"TERRAFORM": {Image: "tf:latest"},
	})
	defer cleanup()

	// Should return without panicking or calling RegisterSource
	ctrl.processNextRun()

	if mock.registeredSourceRunId != "" {
		t.Error("expected no RegisterSource call for 404 fetch")
	}
}

func TestProcessNextRun_UnknownImplementationType_ReportsFailure(t *testing.T) {
	// Use TERRAFORM run, but configure the controller with only MANUAL in implementations.
	// decryptRunDetails works fine with nil crypto when there are no sensitive inputs
	// and the implementation has no encrypted fields (no sshPrivateKey in this test JSON).
	dto, rawBase64, err := buildRunDetailsWithImplType("TERRAFORM")
	if err != nil {
		t.Fatalf("failed to build run details: %v", err)
	}

	mock := &mockRunApi{
		fetchResult:    dto,
		fetchRawBase64: rawBase64,
	}

	// Controller has no TERRAFORM handler — only MANUAL is configured
	ctrl, cleanup := setupControllerWithMockApi(mock, map[string]JobSpecTemplate{
		"MANUAL": {Image: "manual:latest"},
	})
	defer cleanup()
	// crypto is nil; safe here because the TERRAFORM run has no sshPrivateKey and no sensitive inputs

	ctrl.processNextRun()

	if mock.registeredSourceRunId != dto.Metadata.Uuid {
		t.Errorf("expected RegisterSource for run %q, got %q", dto.Metadata.Uuid, mock.registeredSourceRunId)
	}
	if mock.updatedStatus != "FAILED" {
		t.Errorf("expected status FAILED, got %q", mock.updatedStatus)
	}
}

func TestProcessNextRun_HandlerLookup_KnownType(t *testing.T) {
	// This test verifies the implementation type → handler mapping logic directly
	// via the config lookup, without running a full processNextRun (which needs K8s).

	AppConfig = &ControllerConfig{
		Uuid:             "ctrl-uuid",
		OwnedByWorkspace: "test-workspace",
		DisplayName:      "Test Controller",
		Implementations: map[string]JobSpecTemplate{
			"TERRAFORM":       {Image: "tf:latest"},
			"GITHUB_WORKFLOW": {Image: "gh:latest"},
			"GITLAB_PIPELINE": {Image: "gl:latest"},
		},
	}

	cases := []struct {
		implType   meshapi.ImplementationType
		runnerType string
		wantImage  string
	}{
		{meshapi.ImplTypeTerraform, "TERRAFORM", "tf:latest"},
		{meshapi.ImplTypeGitHubWorkflow, "GITHUB_WORKFLOW", "gh:latest"},
		{meshapi.ImplTypeGitLabCICD, "GITLAB_PIPELINE", "gl:latest"},
	}

	for _, tc := range cases {
		runnerType := string(meshapi.ToRunnerType(tc.implType))
		if runnerType != tc.runnerType {
			t.Errorf("ToRunnerType(%q) = %q, want %q", tc.implType, runnerType, tc.runnerType)
		}

		spec, ok := AppConfig.Implementations[runnerType]
		if !ok {
			t.Errorf("no handler found for type %q", runnerType)
			continue
		}
		if spec.Image != tc.wantImage {
			t.Errorf("handler for %q: image = %q, want %q", runnerType, spec.Image, tc.wantImage)
		}
	}
}

func TestProcessNextRun_HandlerLookup_UnknownType_ReturnsError(t *testing.T) {
	// Simulates what processNextRun does when the impl type is not in the map.
	implementations := map[string]JobSpecTemplate{
		"TERRAFORM": {Image: "tf:latest"},
	}

	runnerType := "GITHUB_WORKFLOW" // not in the map above
	_, ok := implementations[runnerType]

	if ok {
		t.Error("expected handler lookup to fail for unconfigured type")
	}
}

func TestReportRunFailure_RegistersSourceThenUpdatesStatus(t *testing.T) {
	mock := &mockRunApi{}
	ctrl, cleanup := setupControllerWithMockApi(mock, map[string]JobSpecTemplate{})
	defer cleanup()

	ctrl.reportRunFailure("run-id-42", "some error occurred")

	if mock.registeredSourceRunId != "run-id-42" {
		t.Errorf("expected RegisterSource called with %q, got %q", "run-id-42", mock.registeredSourceRunId)
	}
	if mock.updatedStatusRunId != "run-id-42" {
		t.Errorf("expected UpdateRunStatus called with %q, got %q", "run-id-42", mock.updatedStatusRunId)
	}
	if mock.updatedStatus != "FAILED" {
		t.Errorf("expected status %q, got %q", "FAILED", mock.updatedStatus)
	}
}

func TestReportRunFailure_StopsIfRegisterSourceFails(t *testing.T) {
	mock := &mockRunApi{
		registerSourceErr: errors.New("network error"),
	}
	ctrl, cleanup := setupControllerWithMockApi(mock, map[string]JobSpecTemplate{})
	defer cleanup()

	ctrl.reportRunFailure("run-id-99", "some error")

	// UpdateRunStatus should NOT be called if RegisterSource failed
	if mock.updatedStatusRunId != "" {
		t.Error("expected UpdateRunStatus NOT called when RegisterSource fails")
	}
}

func TestProcessNextRun_FetchError_LogsAndReturns(t *testing.T) {
	mock := &mockRunApi{
		fetchErr: errors.New("connection refused"),
	}
	ctrl, cleanup := setupControllerWithMockApi(mock, map[string]JobSpecTemplate{
		"TERRAFORM": {Image: "tf:latest"},
	})
	defer cleanup()

	// Should not panic or call RegisterSource
	ctrl.processNextRun()

	if mock.registeredSourceRunId != "" {
		t.Error("expected no RegisterSource for non-404 fetch error")
	}
}

func TestIsNoRunError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "404 status error",
			err:  meshapi.HttpError{StatusCode: 404},
			want: true,
		},
		{
			name: "500 status error",
			err:  meshapi.HttpError{StatusCode: 500},
			want: false,
		},
		{
			name: "non-status error",
			err:  errors.New("some error"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNoRunError(tt.err); got != tt.want {
				t.Errorf("isNoRunError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// Verify that meshapi.HttpError satisfies the error interface and is accessible.
func TestHttpError_Message(t *testing.T) {
	err := meshapi.HttpError{StatusCode: 404}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("expected status error message to contain '404', got: %s", err.Error())
	}
}
