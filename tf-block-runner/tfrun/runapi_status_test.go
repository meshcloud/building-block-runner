package tfrun

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUpdateState_ErrorHandling(t *testing.T) {
	// Test that 403 responses are properly treated as errors
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// API endpoint returns 403
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("forbidden"))
	}))
	defer server.Close()

	// Setup minimal config
	AppConfig = TfRunnerConfig{
		RunnerUuid: "d052263b-7d56-48e0-9886-513809296514",
		RunApiBackend: RunApiConfig{
			Url: server.URL,
		},
	}

	// Create API client with basic auth
	basicAuth := base64.StdEncoding.EncodeToString([]byte("test-user:test-pass"))
	api := &RunApiClient{
		url:    server.URL,
		auth:   "Basic " + basicAuth,
		client: &http.Client{},
	}

	// Create a test run status
	status := &RunStatus{
		RunId: "test-run-id",
		Steps: []*StepStatus{},
	}

	// This should return an error due to 403 status
	abort, err := api.UpdateState(status)

	if err == nil {
		t.Error("Expected error due to 403 response, but got none")
	}

	if statusErr, ok := err.(*StatusError); ok {
		if statusErr.status != 403 {
			t.Errorf("Expected status error 403, got %d", statusErr.status)
		}
	} else {
		t.Errorf("Expected StatusError, got %T: %v", err, err)
	}

	if abort {
		t.Error("Expected abort to be false when error occurs")
	}
}

func TestRegister_ErrorHandling(t *testing.T) {
	// Test that 403 responses are properly treated as errors
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// API endpoint returns 403
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("forbidden"))
	}))
	defer server.Close()

	// Setup minimal config
	AppConfig = TfRunnerConfig{
		RunnerUuid: "d052263b-7d56-48e0-9886-513809296514",
		RunApiBackend: RunApiConfig{
			Url: server.URL,
		},
	}

	// Create API client with basic auth
	basicAuth := base64.StdEncoding.EncodeToString([]byte("test-user:test-pass"))
	api := &RunApiClient{
		url:    server.URL,
		auth:   "Basic " + basicAuth,
		client: &http.Client{},
	}

	// Create a test run status
	status := &RunStatus{
		RunId: "test-run-id",
		Steps: []*StepStatus{},
	}

	// This should return an error due to 403 status
	err := api.Register(status)

	if err == nil {
		t.Error("Expected error due to 403 response, but got none")
	}

	if statusErr, ok := err.(*StatusError); ok {
		if statusErr.status != 403 {
			t.Errorf("Expected status error 403, got %d", statusErr.status)
		}
	} else {
		t.Errorf("Expected StatusError, got %T: %v", err, err)
	}
}
func TestRegister_409Conflict_ReturnsNil(t *testing.T) {
	// Test that a 409 Conflict (source already registered) is treated as success.
	// This can happen when Kubernetes retries a pod: the previous pod already registered
	// the source, so the retry should just continue executing rather than failing.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte("conflict"))
	}))
	defer server.Close()

	AppConfig = TfRunnerConfig{
		RunnerUuid: "d052263b-7d56-48e0-9886-513809296514",
		RunApiBackend: RunApiConfig{
			Url: server.URL,
		},
	}

	basicAuth := base64.StdEncoding.EncodeToString([]byte("test-user:test-pass"))
	api := &RunApiClient{
		url:    server.URL,
		auth:   "Basic " + basicAuth,
		client: &http.Client{},
	}

	status := &RunStatus{
		RunId: "test-run-id",
		Steps: []*StepStatus{},
	}

	// 409 must return nil — registration is idempotent; runner should continue executing
	err := api.Register(status)

	if err != nil {
		t.Errorf("Expected nil error for 409 Conflict, got: %v", err)
	}
}