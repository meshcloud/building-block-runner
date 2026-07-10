package controller

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
)

// setupAppConfigForRegistrationTests points AppConfig at a fake meshfed server and
// returns a restore func, mirroring setupAppConfigForDTOTests (dtos_test.go) but with an
// Api.Url so NewRegistrationApi's meshapi.RunnerClient dials the fake server.
func setupAppConfigForRegistrationTests(apiURL string) func() {
	prev := AppConfig
	AppConfig = &ControllerConfig{
		Uuid:             "test-controller-uuid",
		OwnedByWorkspace: "test-workspace",
		DisplayName:      "Test Controller",
		Namespace:        "test-namespace",
		Api: ApiConfig{
			Url:      apiURL,
			Username: "user",
			Password: "pass",
		},
		Crypto: CryptoConfig{
			PublicKey:  "test-public-key",
			PrivateKey: "test-private-key",
		},
	}
	return func() { AppConfig = prev }
}

func TestRegisterController_PutsUuidWithV1PreviewMediaTypeAndWifBody(t *testing.T) {
	var gotMethod, gotPath, gotAccept, gotContentType string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAccept = r.Header.Get("Accept")
		gotContentType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cleanup := setupAppConfigForRegistrationTests(srv.URL)
	defer cleanup()
	DiscoveredOidcIssuer = "https://oidc.example.com"
	defer func() { DiscoveredOidcIssuer = "" }()

	api := NewRegistrationApi(nopLogger)
	metrics := NewMetricsCollector()

	if err := api.RegisterController(metrics); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotMethod != http.MethodPut {
		t.Errorf("expected PUT, got %s", gotMethod)
	}
	if gotPath != "/api/meshobjects/meshbuildingblockrunners/test-controller-uuid" {
		t.Errorf("unexpected path %q", gotPath)
	}
	if gotAccept != meshapi.RunnerMediaTypeV1Preview || gotContentType != meshapi.RunnerMediaTypeV1Preview {
		t.Errorf("expected v1-preview media type on Accept/Content-Type, got accept=%q contentType=%q", gotAccept, gotContentType)
	}
	if !strings.Contains(string(gotBody), `"implementationType":"ALL"`) {
		t.Errorf("expected the registration body to declare implementationType ALL, got %s", gotBody)
	}
	if !strings.Contains(string(gotBody), "workloadIdentityFederation") {
		t.Errorf("expected WIF block in body when an OIDC issuer is discovered, got %s", gotBody)
	}
}

func TestRegisterController_NotFoundReturnsActionableError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cleanup := setupAppConfigForRegistrationTests(srv.URL)
	defer cleanup()

	api := NewRegistrationApi(nopLogger)
	metrics := NewMetricsCollector()

	err := api.RegisterController(metrics)

	if err == nil {
		t.Fatal("expected an error when the runner meshObject does not exist yet")
	}
	if !strings.Contains(err.Error(), "not found in meshfed") || !strings.Contains(err.Error(), "meshStack UI") {
		t.Errorf("expected the actionable 404 message, got: %v", err)
	}
}

func TestRegisterController_ServerErrorReturnsWrappedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cleanup := setupAppConfigForRegistrationTests(srv.URL)
	defer cleanup()

	api := NewRegistrationApi(nopLogger)
	metrics := NewMetricsCollector()

	err := api.RegisterController(metrics)

	if err == nil {
		t.Fatal("expected an error for a 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected the status code to surface in the error, got: %v", err)
	}
}

func TestRegisterController_SucceedsWithoutWifWhenNoOidcIssuerDiscovered(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cleanup := setupAppConfigForRegistrationTests(srv.URL)
	defer cleanup()
	DiscoveredOidcIssuer = ""

	api := NewRegistrationApi(nopLogger)
	metrics := NewMetricsCollector()

	if err := api.RegisterController(metrics); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(string(gotBody), "workloadIdentityFederation") {
		t.Errorf("expected no WIF block when no OIDC issuer was discovered, got %s", gotBody)
	}
}
