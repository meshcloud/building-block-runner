package mockgithub

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json/v2"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/v2/internal/testacc/mockpipeline"
)

const (
	appId           = 4242
	installationId  = 4711
	owner           = "meshcloud-testacc"
	repository      = "building-block"
	workflowFile    = "apply.yml"
	DestroyWorkflow = "destroy.yml"
	Job             = "deploy"
)

type Options struct {
	mockpipeline.Options

	WithoutActionsWrite bool
	// DeclaredInputs nil accepts every input.
	DeclaredInputs []string
	// WithoutRunDetails answers a dispatch 204 even when it asks for the run details, as an older GitHub Enterprise Server does.
	WithoutRunDetails bool
	// TokenLifetime 0 hands out InstallationToken, which never expires.
	TokenLifetime time.Duration
	RunNotFound   bool
}

// App plays a GitHub App installed on one repository, with the workflows apply.yml and destroy.yml.
type App struct {
	*mockpipeline.Pipeline

	InstallationToken string

	options Options
	baseUrl string
	key     *rsa.PrivateKey
	issued  *issuedTokens
}

func New(t *testing.T, meshStack client.MeshRunnerClient, options Options) App {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	app := App{
		Pipeline:          mockpipeline.New(t, meshStack, options.Options),
		InstallationToken: "ghs_" + rand.Text(),
		options:           options,
		baseUrl:           server.URL,
		key:               key,
		issued:            &issuedTokens{expiry: map[string]time.Time{}},
	}
	mux.HandleFunc("GET /repos/{owner}/{repo}/installation", app.withAppJwt(app.inRepository(app.installation)))
	mux.HandleFunc("POST /app/installations/{installation}/access_tokens", app.withAppJwt(app.accessToken))
	mux.HandleFunc("POST /repos/{owner}/{repo}/actions/workflows/{workflow}/dispatches", app.withInstallationToken(app.inRepository(app.dispatch)))
	mux.HandleFunc("GET /repos/{owner}/{repo}/actions/workflows/{workflow}/runs", app.withInstallationToken(app.inRepository(app.listRuns)))
	mux.HandleFunc("GET /repos/{owner}/{repo}/actions/runs/{run}", app.withInstallationToken(app.inRepository(app.getRun)))
	mux.HandleFunc("GET /repos/{owner}/{repo}/actions/runs/{run}/jobs", app.withInstallationToken(app.inRepository(app.listJobs)))
	mux.HandleFunc("POST /repos/{owner}/{repo}/actions/runs/{run}/cancel", app.withInstallationToken(app.inRepository(app.cancelRun)))
	return app
}

func (a App) Vars(t *testing.T, omitRunObjectInput bool) map[string]any {
	t.Helper()
	return map[string]any{
		"base_url":              a.baseUrl,
		"app_id":                strconv.Itoa(appId),
		"app_private_key":       string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(a.key)})),
		"owner":                 owner,
		"repository":            repository,
		"apply_workflow":        workflowFile,
		"async":                 a.Async,
		"omit_run_object_input": omitRunObjectInput,
	}
}

func (a App) IssuedTokens(t *testing.T) int {
	t.Helper()
	return a.issued.count()
}

func (a App) DispatchUrl(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%s/repos/%s/%s/actions/workflows/%s/dispatches", a.baseUrl, owner, repository, workflowFile)
}

func (a App) withAppJwt(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := a.verifyAppJwt(bearerToken(r)); err != nil {
			mockpipeline.WriteJson(w, http.StatusUnauthorized, map[string]any{"message": "A JSON web token could not be decoded: " + err.Error()})
			return
		}
		next(w, r)
	}
}

func (a App) verifyAppJwt(token string) error {
	header, rest, _ := strings.Cut(token, ".")
	payload, signature, _ := strings.Cut(rest, ".")
	decodedSignature, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(header + "." + payload))
	if verifyErr := rsa.VerifyPKCS1v15(&a.key.PublicKey, crypto.SHA256, digest[:], decodedSignature); verifyErr != nil {
		return fmt.Errorf("the signature does not match the app key: %w", verifyErr)
	}
	decodedPayload, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return err
	}
	var claims struct {
		Iss any   `json:"iss"`
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(decodedPayload, &claims); err != nil {
		return err
	}
	if fmt.Sprint(claims.Iss) != strconv.Itoa(appId) {
		return fmt.Errorf("iss %v is not the app id %d", claims.Iss, appId)
	}
	// GitHub refuses a token that lives longer than 10 minutes.
	if expires := time.Unix(claims.Exp, 0); time.Now().After(expires) || time.Until(expires) > 10*time.Minute {
		return fmt.Errorf("exp %s is not within the next 10 minutes", expires)
	}
	return nil
}

func (a App) withInstallationToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if token := bearerToken(r); token != a.InstallationToken && !a.issued.valid(token) {
			mockpipeline.WriteJson(w, http.StatusUnauthorized, map[string]any{"message": "Bad credentials"})
			return
		}
		next(w, r)
	}
}

func (a App) inRepository(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("owner") != owner || r.PathValue("repo") != repository {
			notFound(w)
			return
		}
		next(w, r)
	}
}

func bearerToken(r *http.Request) string {
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return token
}

func notFound(w http.ResponseWriter) {
	mockpipeline.WriteJson(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
}
