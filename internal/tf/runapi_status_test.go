package tf

import (
	"net/http"
	"net/http/httptest"
	"testing"

	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

func TestUpdateState_ErrorHandling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("forbidden"))
	}))
	defer server.Close()

	AppConfig = TfRunnerConfig{
		RunnerUuid: "d052263b-7d56-48e0-9886-513809296514",
		RunApiBackend: RunApiConfig{
			Url: server.URL,
		},
	}

	auth := &runApiAuth{baseAuth: meshapi.BasicAuth{Username: "test-user", Password: "test-pass"}}
	api := &RunApiClient{
		rid:        AppConfig.RunnerUuid,
		auth:       auth,
		client:     meshapi.NewClient(server.URL, AppConfig.RunnerUuid, auth),
		httpClient: &http.Client{},
	}

	status := &RunStatus{
		RunId: "test-run-id",
		Steps: []StepStatus{},
	}

	abort, err := api.UpdateState(status)

	if err == nil {
		t.Error("Expected error due to 403 response, but got none")
	}

	if abort {
		t.Error("Expected abort to be false when error occurs")
	}
}

func TestRegister_ErrorHandling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("forbidden"))
	}))
	defer server.Close()

	AppConfig = TfRunnerConfig{
		RunnerUuid: "d052263b-7d56-48e0-9886-513809296514",
		RunApiBackend: RunApiConfig{
			Url: server.URL,
		},
	}

	auth := &runApiAuth{baseAuth: meshapi.BasicAuth{Username: "test-user", Password: "test-pass"}}
	api := &RunApiClient{
		rid:        AppConfig.RunnerUuid,
		auth:       auth,
		client:     meshapi.NewClient(server.URL, AppConfig.RunnerUuid, auth),
		httpClient: &http.Client{},
	}

	status := &RunStatus{
		RunId: "test-run-id",
		Steps: []StepStatus{},
	}

	err := api.Register(status)

	if err == nil {
		t.Error("Expected error due to 403 response, but got none")
	}
}

func TestRegister_409Conflict_ReturnsNil(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte("conflict"))
	}))
	defer server.Close()

	AppConfig = TfRunnerConfig{
		RunnerUuid: "d052263b-7d56-48e0-9886-513809296514",
		RunApiBackend: RunApiConfig{
			Url: server.URL,
		},
	}

	auth := &runApiAuth{baseAuth: meshapi.BasicAuth{Username: "test-user", Password: "test-pass"}}
	api := &RunApiClient{
		rid:        AppConfig.RunnerUuid,
		auth:       auth,
		client:     meshapi.NewClient(server.URL, AppConfig.RunnerUuid, auth),
		httpClient: &http.Client{},
	}

	status := &RunStatus{
		RunId: "test-run-id",
		Steps: []StepStatus{},
	}

	err := api.Register(status)

	if err != nil {
		t.Errorf("Expected nil error for 409 Conflict, got: %v", err)
	}
}
