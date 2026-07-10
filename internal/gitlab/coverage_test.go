package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/meshapitest"
)

// TestNewHandler_Defaults exercises NewHandler's nil-defaulting branches (P8: a
// minimally-wired handler is always usable) end to end.
func TestNewHandler_Defaults(t *testing.T) {
	srv := meshapitest.NewServer(t)
	gl := newFakeGitlab(t)
	impl := gitlabImpl(t, gl.URL, "1", "main", "")
	run := buildRun(t, "tok", nil, impl, fullLinks())

	h := NewHandler(Config{Uuid: testUuid}, HandlerDeps{Reporters: factoryFor(srv)})
	require.NoError(t, h.Execute(context.Background(), run))
	require.Equal(t, 1, gl.calls, "the default HTTP client must still reach the fake GitLab")
	require.Len(t, srv.Patches(), 1)
}

// TestNoFollowRedirectClient_ActuallyDoesNotFollow drives the CheckRedirect function
// directly against a real 3xx-with-Location response (G-P10), rather than only through
// the fake GitLab helper (which never sets Location).
func TestNoFollowRedirectClient_ActuallyDoesNotFollow(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("the redirect target must never be reached")
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	resp, err := noFollowRedirectClient().Get(redirector.URL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusFound, resp.StatusCode)
}

// TestDecodeRawRunJson_Errors covers Execute's row-C mapping of a missing/invalid raw
// payload (decodeRawRunJson's error branches).
func TestDecodeRawRunJson_Errors(t *testing.T) {
	cases := []struct {
		name    string
		rawJson string
	}{
		{"empty raw JSON", ""},
		{"invalid base64", "!!!not-base64!!!"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := meshapitest.NewServer(t)
			impl := gitlabImpl(t, "http://unused.invalid", "1", "main", "tok")
			dto := &meshapi.RunDetailsDTO{
				Metadata: meshapi.RunMetaDTO{Uuid: testUuid},
				Spec:     meshapi.RunSpecDTO{Definition: meshapi.DefinitionSpecDTO{Spec: meshapi.DefinitionDetailsSpecDTO{Implementation: impl}}},
			}
			run := dispatch.ClaimedRun{Id: dispatch.RunId(testUuid), Details: dto, RawJson: c.rawJson}

			h := NewHandler(Config{Uuid: testUuid}, HandlerDeps{Reporters: factoryFor(srv), HTTP: httpClient(), Log: testLog()})
			require.NoError(t, h.Execute(context.Background(), run))
			upd := decodePatch(t, srv.Patches()[0].Body)
			require.Equal(t, "FAILED", upd.Status)
			require.Contains(t, upd.Steps[0].SystemMessage, "internal error")
		})
	}
}

// TestDecodeImplementation_Errors covers the nil-Details, unreadable-type, and
// field-type-mismatch branches of decodeImplementation directly.
func TestDecodeImplementation_Errors(t *testing.T) {
	t.Run("nil details", func(t *testing.T) {
		_, err := decodeImplementation(dispatch.ClaimedRun{Id: "r1"})
		require.Error(t, err)
	})

	t.Run("implementation is not a JSON object", func(t *testing.T) {
		dto := &meshapi.RunDetailsDTO{
			Spec: meshapi.RunSpecDTO{Definition: meshapi.DefinitionSpecDTO{Spec: meshapi.DefinitionDetailsSpecDTO{
				Implementation: json.RawMessage(`123`),
			}}},
		}
		_, err := decodeImplementation(dispatch.ClaimedRun{Id: "r1", Details: dto})
		require.Error(t, err)
	})

	t.Run("field type mismatch after type check passes", func(t *testing.T) {
		dto := &meshapi.RunDetailsDTO{
			Spec: meshapi.RunSpecDTO{Definition: meshapi.DefinitionSpecDTO{Spec: meshapi.DefinitionDetailsSpecDTO{
				Implementation: json.RawMessage(`{"type":"GITLAB_CICD","projectId":123}`),
			}}},
		}
		_, err := decodeImplementation(dispatch.ClaimedRun{Id: "r1", Details: dto})
		require.Error(t, err)
	})
}

// TestBuildTriggerForm_MalformedJSON covers buildTriggerForm's own decode-error branch
// directly (unreachable through Execute, since meshapi.DecryptInputs always re-marshals
// valid JSON on success).
func TestBuildTriggerForm_MalformedJSON(t *testing.T) {
	_, _, err := buildTriggerForm("tok", "main", []byte("{not json"), meshapi.LinksDTO{}, testLog())
	require.Error(t, err)
}

// TestSanitizeBaseUrl_InvalidURL covers the url.Parse error branch with a malformed
// percent-escape, which trim/empty-checks alone would not catch.
func TestSanitizeBaseUrl_InvalidURL(t *testing.T) {
	_, err := sanitizeBaseUrl("http://%zz")
	require.Error(t, err)
}

// TestCompactJSON_FallsBackOnInvalidInput covers compactJSON's defensive fallback branch.
func TestCompactJSON_FallsBackOnInvalidInput(t *testing.T) {
	require.Equal(t, "not json", compactJSON([]byte("not json")))
}

// brokenTransport always fails at the transport level (DNS/connection-refused twin),
// covering triggerPipeline's httpClient.Do error branch (-> Execute's row-C mapping).
type brokenTransport struct{}

func (brokenTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, &net.OpError{Op: "dial", Err: errors.New("simulated transport failure")}
}

// TestScenario_Gitlab_TriggerFails_TransportError covers a network-level failure reaching
// GitLab (not an HTTP-status failure): row-C, not row-B (no *ExternalCallError to classify).
func TestScenario_Gitlab_TriggerFails_TransportError(t *testing.T) {
	srv := meshapitest.NewServer(t)
	impl := gitlabImpl(t, "http://unused.invalid", "1", "main", "tok")
	run := buildRun(t, "tok", nil, impl, fullLinks())

	h := NewHandler(Config{Uuid: testUuid}, HandlerDeps{
		Reporters: factoryFor(srv),
		HTTP:      &http.Client{Transport: brokenTransport{}},
		Log:       testLog(),
	})
	require.NoError(t, h.Execute(context.Background(), run))
	upd := decodePatch(t, srv.Patches()[0].Body)
	require.Equal(t, "FAILED", upd.Status)
	require.Contains(t, upd.Steps[0].SystemMessage, "internal error")
}

// TestRunSingleRun_MalformedRunFile covers RunSingleRun's ParseRunDetails error branch.
func TestRunSingleRun_MalformedRunFile(t *testing.T) {
	runFile := t.TempDir() + "/run.json"
	require.NoError(t, os.WriteFile(runFile, []byte("{not json"), 0o600))
	t.Setenv(envRunJsonFilePath, runFile)

	cfg := Config{Uuid: testUuid, Api: config.Api{Url: "http://unused.invalid"}}
	require.Equal(t, 1, RunSingleRun(context.Background(), testLog(), cfg, meshapi.Identity{}))
}

// TestLoadConfig_BlockRunnerPrivateKeyFile covers the blockrunner.privateKeyFile compat
// branch (as opposed to the inline blockrunner.privateKey already covered elsewhere).
func TestLoadConfig_BlockRunnerPrivateKeyFile(t *testing.T) {
	absentBase(t)
	keyFile := t.TempDir() + "/mounted-key.pem"
	require.NoError(t, os.WriteFile(keyFile, []byte("mounted-key-content"), 0o600))
	writeConfig(t, "blockrunner:\n  privateKeyFile: \""+keyFile+"\"\n")
	t.Setenv("RUNNER_PRIVATE_KEY_FILE", "") // do not let env override the yaml-provided path

	cfg, err := LoadConfig(testLog(), "v", false)
	require.NoError(t, err)
	require.Equal(t, "mounted-key-content", cfg.PrivateKeyPEM)
}
