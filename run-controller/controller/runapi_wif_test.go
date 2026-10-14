package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_WifTokenMissing_FailedStepOfOwnSourceWithoutRunStatus(t *testing.T) {
	requests := map[string]map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		requests[r.Method+" "+r.URL.Path] = body
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	prev := AppConfig
	AppConfig = &ControllerConfig{Uuid: "controller-uuid", Api: ApiConfig{Url: server.URL, Username: "u", Password: "p"}}
	t.Cleanup(func() { AppConfig = prev })

	err := newRunApiClient().ReportWifTokenMissing(context.Background(), "run-1", "admin login: Keycloak returned HTTP 503")

	require.NoError(t, err)
	registration := requests["POST /api/meshobjects/meshbuildingblockruns/run-1/status/source"]
	require.NotNil(t, registration)
	assert.Equal(t, "controller-uuid-wif-token", registration["source"].(map[string]any)["id"])

	update := requests["PATCH /api/meshobjects/meshbuildingblockruns/run-1/status/source/controller-uuid-wif-token"]
	require.NotNil(t, update)
	assert.Nil(t, update["status"])
	step := update["steps"].([]any)[0].(map[string]any)
	assert.Equal(t, "FAILED", step["status"])
	assert.Contains(t, step["systemMessage"], "admin login: Keycloak returned HTTP 503")
	assert.Contains(t, step["systemMessage"], "/var/run/secrets/workload-identity/meshstack/token")
	assert.NotContains(t, step, "userMessage")
}
