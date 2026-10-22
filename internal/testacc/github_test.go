package testacc

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
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/require"
)

const (
	githubAppId          = 4242
	githubInstallationId = 4711
	githubOwner          = "meshcloud-testacc"
	githubRepository     = "building-block"
	githubWorkflowFile   = "apply.yml"
	githubJob            = "deploy"
)

type githubOptions struct {
	outcome             string
	async               bool
	withoutActionsWrite bool
	// declaredInputs nil accepts every input.
	declaredInputs []string
	// withoutRunDetails answers a dispatch 204 even when it asks for the run details, as an older GitHub Enterprise Server does.
	withoutRunDetails bool
}

type githubApp struct {
	*pipeline

	options           githubOptions
	baseUrl           string
	key               *rsa.PrivateKey
	installationToken string
}

func (m *pipelineMock) newGitHubApp(t *testing.T, options githubOptions) *githubApp {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	app := &githubApp{pipeline: m.newPipeline(t, options.outcome, options.async), options: options, key: key, installationToken: "ghs_" + rand.Text()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/{owner}/{repo}/installation", app.withAppJwt(app.inRepository(app.installation)))
	mux.HandleFunc("POST /app/installations/{installation}/access_tokens", app.withAppJwt(app.accessToken))
	mux.HandleFunc("POST /repos/{owner}/{repo}/actions/workflows/{workflow}/dispatches", app.withInstallationToken(app.inRepository(app.dispatch)))
	mux.HandleFunc("GET /repos/{owner}/{repo}/actions/workflows/{workflow}/runs", app.withInstallationToken(app.inRepository(app.listRuns)))
	mux.HandleFunc("GET /repos/{owner}/{repo}/actions/runs/{run}", app.withInstallationToken(app.inRepository(app.getRun)))
	mux.HandleFunc("GET /repos/{owner}/{repo}/actions/runs/{run}/jobs", app.withInstallationToken(app.inRepository(app.listJobs)))
	mux.HandleFunc("POST /repos/{owner}/{repo}/actions/runs/{run}/cancel", app.withInstallationToken(app.inRepository(app.cancelRun)))
	app.baseUrl = m.mount(mux)
	return app
}

func (a *githubApp) vars(omitRunObjectInput bool) map[string]any {
	return map[string]any{
		"base_url":              a.baseUrl,
		"app_id":                strconv.Itoa(githubAppId),
		"app_private_key":       string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(a.key)})),
		"owner":                 githubOwner,
		"repository":            githubRepository,
		"apply_workflow":        githubWorkflowFile,
		"async":                 a.async,
		"omit_run_object_input": omitRunObjectInput,
	}
}

func (a *githubApp) withAppJwt(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := a.verifyAppJwt(bearerToken(r)); err != nil {
			writeJson(w, http.StatusUnauthorized, map[string]any{"message": "A JSON web token could not be decoded: " + err.Error()})
			return
		}
		next(w, r)
	}
}

func (a *githubApp) verifyAppJwt(token string) error {
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
	if fmt.Sprint(claims.Iss) != strconv.Itoa(githubAppId) {
		return fmt.Errorf("iss %v is not the app id %d", claims.Iss, githubAppId)
	}
	// GitHub refuses a token that lives longer than 10 minutes.
	if expires := time.Unix(claims.Exp, 0); time.Now().After(expires) || time.Until(expires) > 10*time.Minute {
		return fmt.Errorf("exp %s is not within the next 10 minutes", expires)
	}
	return nil
}

func (a *githubApp) withInstallationToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if bearerToken(r) != a.installationToken {
			writeJson(w, http.StatusUnauthorized, map[string]any{"message": "Bad credentials"})
			return
		}
		next(w, r)
	}
}

func (a *githubApp) inRepository(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("owner") != githubOwner || r.PathValue("repo") != githubRepository {
			writeJson(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
			return
		}
		next(w, r)
	}
}

func bearerToken(r *http.Request) string {
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return token
}

func (a *githubApp) installation(w http.ResponseWriter, _ *http.Request) {
	writeJson(w, http.StatusOK, map[string]any{
		"id": githubInstallationId, "app_id": githubAppId, "client_id": "Iv1.testacc", "target_type": "Organization",
	})
}

func (a *githubApp) accessToken(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("installation") != strconv.Itoa(githubInstallationId) {
		writeJson(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return
	}
	actions := "write"
	if a.options.withoutActionsWrite {
		actions = "read"
	}
	writeJson(w, http.StatusCreated, map[string]any{
		"token":                a.installationToken,
		"expires_at":           time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		"permissions":          map[string]string{"actions": actions, "metadata": "read"},
		"repository_selection": "selected",
	})
}

func (a *githubApp) dispatch(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("workflow") != githubWorkflowFile {
		writeJson(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return
	}
	var body struct {
		Ref              string            `json:"ref"`
		Inputs           map[string]string `json:"inputs"`
		ReturnRunDetails bool              `json:"return_run_details"`
	}
	if err := json.UnmarshalRead(r.Body, &body); err != nil {
		writeJson(w, http.StatusBadRequest, map[string]any{"message": "Problems parsing JSON"})
		return
	}
	if unexpected := a.unexpectedInputs(body.Inputs); len(unexpected) > 0 {
		names, _ := json.Marshal(unexpected)
		writeJson(w, http.StatusUnprocessableEntity, map[string]any{"message": "Unexpected inputs provided: " + string(names)})
		return
	}
	run, runErr := githubRunOf(body.Inputs)
	started := a.start(r.Context(), pipelineTrigger{ref: body.Ref, params: body.Inputs}, run, runErr)
	if !body.ReturnRunDetails || a.options.withoutRunDetails {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJson(w, http.StatusOK, map[string]any{
		"workflow_run_id": started.id,
		"run_url":         fmt.Sprintf("%s/repos/%s/%s/actions/runs/%d", a.baseUrl, githubOwner, githubRepository, started.id),
		"html_url":        a.htmlUrl(started),
	})
}

func (a *githubApp) unexpectedInputs(inputs map[string]string) []string {
	var unexpected []string
	for name := range inputs {
		if a.options.declaredInputs != nil && !slices.Contains(a.options.declaredInputs, name) {
			unexpected = append(unexpected, name)
		}
	}
	slices.Sort(unexpected)
	return unexpected
}

func githubRunOf(inputs map[string]string) (client.MeshRunnerRun, error) {
	encoded, withRunObject := inputs["buildingBlockRun"]
	if !withRunObject {
		return runAt(inputs["buildingBlockRunUrl"], inputs["MESHSTACK_RUN_TOKEN"])
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return client.MeshRunnerRun{}, err
	}
	return decodeRun(decoded)
}

func (a *githubApp) listRuns(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("workflow") != githubWorkflowFile {
		writeJson(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return
	}
	var runs []map[string]any
	for run := range a.newestRuns {
		runs = append(runs, a.runJson(run))
	}
	writeJson(w, http.StatusOK, map[string]any{"total_count": len(runs), "workflow_runs": runs})
}

func (a *githubApp) getRun(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("run"), 10, 64)
	run, found := a.poll(id)
	if !found {
		writeJson(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return
	}
	writeJson(w, http.StatusOK, a.runJson(run))
}

func (a *githubApp) listJobs(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("run"), 10, 64)
	run, found := a.peek(id)
	if !found {
		writeJson(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return
	}
	job := a.runJson(run)
	job["run_id"] = run.id
	job["name"] = githubJob
	job["started_at"] = run.created.Format(time.RFC3339)
	job["completed_at"] = nil
	if job["conclusion"] != nil {
		job["completed_at"] = job["updated_at"]
	}
	writeJson(w, http.StatusOK, map[string]any{"total_count": 1, "jobs": []map[string]any{job}})
}

func (a *githubApp) cancelRun(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("run"), 10, 64)
	run, found := a.peek(id)
	if !found {
		writeJson(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return
	}
	if _, completed := a.progress(run, githubStatuses); completed {
		writeJson(w, http.StatusConflict, map[string]any{"message": "Cannot cancel a workflow run that is completed."})
		return
	}
	a.cancel(id)
	writeJson(w, http.StatusAccepted, map[string]any{})
}

// githubStatuses holds waiting, which GitHub sends while a job waits for a runner or an approval.
var githubStatuses = []string{"queued", "waiting", "in_progress", "completed"}

func (a *githubApp) runJson(run pipelineRun) map[string]any {
	status, completed := a.progress(run, githubStatuses)
	var conclusion any
	switch {
	case !completed:
	case run.canceled:
		conclusion = "cancelled"
	case a.succeeds():
		conclusion = "success"
	default:
		conclusion = "failure"
	}
	return map[string]any{
		"id":         run.id,
		"status":     status,
		"conclusion": conclusion,
		"created_at": run.created.Format(time.RFC3339),
		"updated_at": run.created.Add(time.Duration(run.polls) * time.Second).Format(time.RFC3339),
		"html_url":   a.htmlUrl(run),
	}
}

func (a *githubApp) htmlUrl(run pipelineRun) string {
	return fmt.Sprintf("%s/%s/%s/actions/runs/%d", a.baseUrl, githubOwner, githubRepository, run.id)
}
