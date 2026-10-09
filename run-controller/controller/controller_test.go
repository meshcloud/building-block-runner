package controller

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"os"
	"strings"
	"testing"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
)

type mockRunApi struct {
	fetchResult       *meshapi.RunDetailsDTO
	fetchRawBase64    string
	fetchErr          error
	registerSourceErr error
	updateStatusErr   error

	registeredSourceRunId string
	updatedStatusRunId    string
	updatedStatus         string
	updatedSummary        string
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
	m.updatedSummary = summary
	return m.updateStatusErr
}

// The run has no sensitive inputs, so decryptRunDetails needs no crypto.
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
		logger:     log.New(os.Stdout, "[TEST] ", 0),
		runApi:     mock,
		metrics:    NewMetricsCollector(),
		dispatcher: &fakeDispatcher{},
	}

	return ctrl, func() { AppConfig = prev }
}

func TestProcessNextRun_NoRunAvailable(t *testing.T) {
	mock := &mockRunApi{
		fetchErr: &meshapi.StatusError{Status: 404},
	}
	ctrl, cleanup := setupControllerWithMockApi(mock, map[string]JobSpecTemplate{
		"TERRAFORM": {Image: "tf:latest"},
	})
	defer cleanup()

	ctrl.processNextRun()

	if mock.registeredSourceRunId != "" {
		t.Error("expected no RegisterSource call for 404 fetch")
	}
}

func TestProcessNextRun_DispatchFails_ReportsFailure(t *testing.T) {
	dto, rawBase64, err := buildRunDetailsWithImplType("TERRAFORM")
	if err != nil {
		t.Fatalf("failed to build run details: %v", err)
	}
	mock := &mockRunApi{fetchResult: dto, fetchRawBase64: rawBase64}
	ctrl, cleanup := setupControllerWithMockApi(mock, nil)
	defer cleanup()
	ctrl.dispatcher = &fakeDispatcher{dispatchErr: &NoHandlerError{RunnerType: "TERRAFORM"}}

	if got := ctrl.processNextRun(); got != processFailed {
		t.Errorf("expected processFailed, got %v", got)
	}
	if mock.registeredSourceRunId != dto.Metadata.Uuid {
		t.Errorf("expected RegisterSource for run %q, got %q", dto.Metadata.Uuid, mock.registeredSourceRunId)
	}
	if mock.updatedStatus != "FAILED" {
		t.Errorf("expected status FAILED, got %q", mock.updatedStatus)
	}
	if want := "no implementation handler configured for type 'TERRAFORM'"; mock.updatedSummary != want {
		t.Errorf("expected summary %q, got %q", want, mock.updatedSummary)
	}
}

func TestKubernetesDispatcher_KnownType_CreatesJobWithItsSpec(t *testing.T) {
	_, cleanup := setupControllerWithMockApi(&mockRunApi{}, map[string]JobSpecTemplate{
		"TERRAFORM":       {Image: "tf:latest"},
		"GITHUB_WORKFLOW": {Image: "gh:latest"},
	})
	defer cleanup()
	jobs := &fakeJobManager{}
	dispatcher := kubernetesDispatcher{jobs: jobs, metrics: NewMetricsCollector()}

	if err := dispatcher.Dispatch(&meshapi.RunDetailsDTO{}, "", "GITHUB_WORKFLOW"); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if jobs.lastImage != "gh:latest" {
		t.Errorf("expected job with image gh:latest, got %q", jobs.lastImage)
	}
}

func TestKubernetesDispatcher_UnknownType_ReturnsNoHandlerError(t *testing.T) {
	_, cleanup := setupControllerWithMockApi(&mockRunApi{}, map[string]JobSpecTemplate{
		"TERRAFORM": {Image: "tf:latest"},
	})
	defer cleanup()
	jobs := &fakeJobManager{}
	dispatcher := kubernetesDispatcher{jobs: jobs, metrics: NewMetricsCollector()}

	err := dispatcher.Dispatch(&meshapi.RunDetailsDTO{}, "", "GITHUB_WORKFLOW")

	if _, ok := errors.AsType[*NoHandlerError](err); !ok {
		t.Errorf("expected NoHandlerError, got %v", err)
	}
	if jobs.createCalls != 0 {
		t.Errorf("expected no job created, got %d", jobs.createCalls)
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
			err:  &meshapi.StatusError{Status: 404},
			want: true,
		},
		{
			name: "500 status error",
			err:  &meshapi.StatusError{Status: 500},
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

func TestStatusError_Message(t *testing.T) {
	err := &meshapi.StatusError{Status: 404}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("expected status error message to contain '404', got: %s", err.Error())
	}
}
