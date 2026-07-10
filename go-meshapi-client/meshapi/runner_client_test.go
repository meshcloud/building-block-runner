package meshapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewRunnerClient_UsesDefaultHTTPClient(t *testing.T) {
	client := NewRunnerClient("http://example.invalid", BasicAuth{Username: "u", Password: "p"})

	if client.http == nil {
		t.Fatal("expected a default http.Client to be set")
	}
}

func TestRunnerClientUpdate_InvalidBaseURLReturnsRequestConstructionError(t *testing.T) {
	client := NewRunnerClient("http://[::1]:namedport", BasicAuth{})

	_, err := client.Update("runner-uuid-1", []byte(`{}`))

	if err == nil {
		t.Fatal("expected an error constructing the request from a malformed base URL")
	}
	if !strings.Contains(err.Error(), "failed to create PUT request") {
		t.Errorf("expected a request-construction error, got: %v", err)
	}
}

func TestRunnerClientUpdate_TransportErrorIsWrapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := srv.URL
	srv.Close() // closed before use, so the request fails to dial

	client := NewRunnerClient(closedURL, BasicAuth{})

	_, err := client.Update("runner-uuid-1", []byte(`{}`))

	if err == nil {
		t.Fatal("expected an error when the transport fails to dial")
	}
	if !strings.Contains(err.Error(), "failed to execute PUT request") {
		t.Errorf("expected a transport-execution error, got: %v", err)
	}
}

func TestRunnerClientUpdate_SendsPutWithV1PreviewMediaTypeAndBody(t *testing.T) {
	type capturedRequest struct {
		method      string
		url         string
		accept      string
		contentType string
		auth        string
		body        []byte
	}
	var got capturedRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method = r.Method
		got.url = r.URL.Path
		got.accept = r.Header.Get("Accept")
		got.contentType = r.Header.Get("Content-Type")
		got.auth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		got.body = body
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewRunnerClientWithHTTP(srv.URL, BasicAuth{Username: "u", Password: "p"}, srv.Client())

	statusCode, err := client.Update("runner-uuid-1", []byte(`{"kind":"meshBuildingBlockRunner"}`))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if statusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", statusCode)
	}
	if got.method != http.MethodPut {
		t.Errorf("expected PUT, got %s", got.method)
	}
	if got.url != "/api/meshobjects/meshbuildingblockrunners/runner-uuid-1" {
		t.Errorf("unexpected URL path %q", got.url)
	}
	if got.accept != RunnerMediaTypeV1Preview {
		t.Errorf("expected Accept %q, got %q", RunnerMediaTypeV1Preview, got.accept)
	}
	if got.contentType != RunnerMediaTypeV1Preview {
		t.Errorf("expected Content-Type %q, got %q", RunnerMediaTypeV1Preview, got.contentType)
	}
	if !strings.HasPrefix(got.auth, "Basic ") {
		t.Errorf("expected Basic auth header, got %q", got.auth)
	}
	var echoed map[string]string
	if err := json.Unmarshal(got.body, &echoed); err != nil {
		t.Fatalf("body should be forwarded verbatim: %v", err)
	}
	if echoed["kind"] != "meshBuildingBlockRunner" {
		t.Errorf("body content not forwarded correctly, got %v", echoed)
	}
}

func TestRunnerClientUpdate_NotFoundReturnsNoErrorWithStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := NewRunnerClientWithHTTP(srv.URL, BasicAuth{}, srv.Client())

	statusCode, err := client.Update("runner-uuid-1", []byte(`{}`))

	if err != nil {
		t.Fatalf("404 should not surface as an error from the transport, got: %v", err)
	}
	if statusCode != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", statusCode)
	}
}

func TestRunnerClientUpdate_NonOkNon404ReturnsErrorWithBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error detail", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := NewRunnerClientWithHTTP(srv.URL, BasicAuth{}, srv.Client())

	statusCode, err := client.Update("runner-uuid-1", []byte(`{}`))

	if err == nil {
		t.Fatal("expected an error for a non-200/404 response")
	}
	if statusCode != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", statusCode)
	}
	if !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "internal error detail") {
		t.Errorf("expected error to surface status and body, got: %v", err)
	}
}
