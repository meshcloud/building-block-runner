package gitlab

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/httpclient"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/meshapitest"
	"github.com/meshcloud/building-block-runner/internal/runmode"
)

const testUuid = "bfe76555-7a69-48e8-8cc0-8e02eb76fc22"

// envRunJsonFilePath mirrors runmode.RunJsonFilePathEnv; kept as this package's own name
// because these single-run scenario tests still reference it directly.
const envRunJsonFilePath = runmode.RunJsonFilePathEnv

// runSingleRunForTest mirrors what cmd/bbrunner's gitlab single-run bootstrap wires: the same
// Handler polling uses, run once via runmode.SingleRunFromFile against RUN_JSON_FILE_PATH.
func runSingleRunForTest(ctx context.Context, log *slog.Logger, cfg Config, id meshapi.Identity) int {
	handler := NewHandler(cfg, HandlerDeps{
		Reporters: NewReporterFactory(cfg.Api.Url, cfg.Uuid, id, log),
		Log:       log,
	})

	return runmode.SingleRunFromFile(ctx, log, cfg.Uuid, meshapi.RunnerTypeGitLabPipeline,
		func(ctx context.Context, run dispatch.ClaimedRun) error {
			return handler.Execute(ctx, run)
		})
}

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func factoryFor(srv *meshapitest.Server) ReporterFactory {
	return NewReporterFactory(srv.URL, testUuid, meshapi.Identity{Name: "gitlab-block-runner", Version: "test"}, testLog())
}

func decodePatch(t *testing.T, body []byte) meshapi.SourceUpdateDTO {
	t.Helper()
	var u meshapi.SourceUpdateDTO
	require.NoError(t, json.Unmarshal(body, &u))
	return u
}

// gitlabImpl builds a GitlabImplementation impl JSON payload for embedding in a run
// fixture's buildingBlockDefinition.spec.implementation.
func gitlabImpl(t *testing.T, baseUrl, projectId, refName, token string) json.RawMessage {
	t.Helper()
	impl := meshapi.GitlabImplementation{
		Type:                 string(meshapi.ImplTypeGitLabCICD),
		GitlabBaseUrl:        baseUrl,
		ProjectId:            projectId,
		RefName:              refName,
		PipelineTriggerToken: token,
	}
	raw, err := json.Marshal(impl)
	require.NoError(t, err)
	return raw
}

// buildRun assembles a ClaimedRun with a full link set and the given GitLab impl, the way
// the runner type would (Details parsed + RawJson = base64 of the same bytes, so
// SanitizeRunObjectForHandover re-reads the raw bytes for fidelity). The run JSON is
// built already-decrypted, matching the dispatch-boundary contract Execute now relies on.
func buildRun(t *testing.T, token string, inputs []meshapi.BuildingBlockInputSpecDTO, impl json.RawMessage, links meshapi.LinksDTO) dispatch.ClaimedRun {
	t.Helper()
	dto := &meshapi.Run{
		Metadata: meshapi.RunMetaDTO{Uuid: testUuid},
		Spec: meshapi.RunSpecDTO{
			RunToken:      token,
			Behavior:      "APPLY",
			BuildingBlock: meshapi.BuildingBlockSpecDTO{Spec: meshapi.BuildingBlockDetailsSpecDTO{Inputs: inputs}},
			Definition:    meshapi.DefinitionSpecDTO{Spec: meshapi.DefinitionDetailsSpecDTO{Implementation: impl}},
		},
		Links: links,
	}
	raw, err := json.Marshal(dto)
	require.NoError(t, err)
	return dispatch.ClaimedRun{
		Id:      dispatch.RunId(testUuid),
		Type:    meshapi.RunnerTypeGitLabPipeline,
		Run:     dto,
		RawJson: base64.StdEncoding.EncodeToString(raw),
	}
}

// meshstackBase is the fixture coordinator base URL every fullLinks() call uses.
const meshstackBase = "http://mesh.example"

func fullLinks() meshapi.LinksDTO {
	return meshapi.LinksDTO{
		Self:             meshapi.LinkDTO{Href: meshstackBase + "/api/meshobjects/meshbuildingblockruns/" + testUuid},
		RegisterSource:   meshapi.LinkDTO{Href: meshstackBase + "/api/meshobjects/meshbuildingblockruns/" + testUuid + "/status/source"},
		UpdateSource:     meshapi.LinkDTO{Href: meshstackBase + "/api/meshobjects/meshbuildingblockruns/" + testUuid + "/status/source/{sourceId}", Templated: true},
		MeshstackBaseUrl: meshapi.LinkDTO{Href: meshstackBase},
	}
}

// fakeGitlab is an httptest-backed stand-in for the GitLab trigger endpoint: it captures
// the multipart form and the request (redirect-following, method, path) and returns a
// scripted response.
type fakeGitlab struct {
	*httptest.Server
	status int
	body   string

	lastPath   string
	lastMethod string
	lastForm   map[string][]string
	calls      int
}

func newFakeGitlab(t *testing.T) *fakeGitlab {
	t.Helper()
	fg := &fakeGitlab{status: http.StatusOK}
	fg.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fg.calls++
		fg.lastPath = r.URL.Path
		fg.lastMethod = r.Method
		// t.Errorf (not require.NoError) — this handler runs on its own goroutine, where a
		// fatal assertion (t.FailNow) is unsafe (testifylint go-require).
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Errorf("fake gitlab: parsing multipart form: %v", err)
			w.WriteHeader(fg.status)
			return
		}
		fg.lastForm = map[string][]string{}
		for k, v := range r.MultipartForm.Value {
			fg.lastForm[k] = v
		}
		w.WriteHeader(fg.status)
		_, _ = w.Write([]byte(fg.body))
	}))
	t.Cleanup(fg.Close)
	return fg
}

func (fg *fakeGitlab) formValue(name string) string {
	vals := fg.lastForm[name]
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

// httpClient returns an *http.Client wired with the sentinel CheckRedirect the shared
// singleton uses, so triggerPipeline's WithNoRedirect (per-request, not per-client) is
// honored the same way it is in production.
func httpClient() *http.Client {
	return &http.Client{CheckRedirect: httpclient.SentinelCheckRedirect}
}

// TestScenario_Gitlab_PollingRun_TriggersAndHandsOver pins the core happy path:
// register -> one multipart POST (token/ref/behavior/MESHSTACK_RUN + env/non-env inputs +
// all four callback URLs) -> one IN_PROGRESS handover PATCH, nothing else.
func TestScenario_Gitlab_PollingRun_TriggersAndHandsOver(t *testing.T) {
	srv := meshapitest.NewServer(t)
	gl := newFakeGitlab(t)

	inputs := []meshapi.BuildingBlockInputSpecDTO{
		{Key: "envInput", Value: "testEnv", Type: "STRING", Env: true},
		{Key: "inputInput", Value: "testInput", Type: "STRING", Env: false},
		{Key: "count", Value: json.Number("4"), Type: "INTEGER", Env: true},
		{Key: "flag", Value: true, Type: "BOOLEAN", Env: true},
	}
	impl := gitlabImpl(t, gl.URL, "1111111", "refName", "TOKEN")
	run := buildRun(t, "run-token-xyz", inputs, impl, fullLinks())

	h := NewHandler(Config{BaseConfig: config.BaseConfig{Uuid: testUuid}}, HandlerDeps{
		Reporters: factoryFor(srv),
		HTTP:      httpClient(),
		Log:       testLog(),
	})
	require.NoError(t, h.Execute(context.Background(), run))

	// One register, exactly one PATCH (the handover) -- ordering + cardinality.
	regs := srv.Registers()
	require.Len(t, regs, 1)
	require.Len(t, regs[0].Registration.Steps, 1)
	require.Equal(t, StepId, regs[0].Registration.Steps[0].Id)
	require.Equal(t, StepDisplayName, regs[0].Registration.Steps[0].DisplayName)

	require.Equal(t, 1, gl.calls)
	require.Equal(t, "/api/v4/projects/1111111/trigger/pipeline", gl.lastPath)
	require.Equal(t, "TOKEN", gl.formValue("token"))
	require.Equal(t, "refName", gl.formValue("ref"))
	require.Equal(t, "APPLY", gl.formValue("variables[MESHSTACK_BEHAVIOR]"))
	require.Equal(t, "testEnv", gl.formValue("variables[envInput]"))
	require.Equal(t, "testInput", gl.formValue("inputs[inputInput]"))
	require.Equal(t, "4", gl.formValue("variables[count]"))
	require.Equal(t, "true", gl.formValue("variables[flag]"))
	require.Equal(t, "http://mesh.example/api/meshobjects/meshbuildingblockruns/"+testUuid, gl.formValue("variables[MESHSTACK_SELF_URL]"))
	require.Equal(t, "http://mesh.example/api/meshobjects/meshbuildingblockruns/"+testUuid+"/status/source", gl.formValue("variables[MESHSTACK_REGISTER_SOURCE_URL]"))
	require.Equal(t, "http://mesh.example/api/meshobjects/meshbuildingblockruns/"+testUuid+"/status/source/{sourceId}", gl.formValue("variables[MESHSTACK_UPDATE_SOURCE_URL]"))
	require.Equal(t, meshstackBase, gl.formValue("variables[MESHSTACK_BASE_URL]"))
	require.NotEmpty(t, gl.formValue("variables[MESHSTACK_RUN]"))

	patches := srv.Patches()
	require.Len(t, patches, 1)
	upd := decodePatch(t, patches[0].Body)
	require.Equal(t, "IN_PROGRESS", upd.Status)
	require.Len(t, upd.Steps, 1)
	require.Equal(t, StepId, upd.Steps[0].Id)
	require.Equal(t, "SUCCEEDED", upd.Steps[0].Status)
	require.Equal(t, userMessageHandover, upd.Steps[0].UserMessage)
	require.Equal(t, "Triggered pipeline in project '1111111'", upd.Steps[0].SystemMessage)
}

// TestScenario_Gitlab_BackendAbort pins T1 for gitlab: the trigger succeeds, but the
// backend flags runAborted on the handover PATCH response. gitlab has no poll loop to
// interrupt, so it acts on the abort by following the would-be IN_PROGRESS handover with a
// terminal ABORTED update -- never leaving the run stuck IN_PROGRESS, and never erroring.
func TestScenario_Gitlab_BackendAbort(t *testing.T) {
	srv := meshapitest.NewServer(t)
	srv.SeedPatchResponse(meshapitest.PatchResponse{Status: 200, Abort: true})
	gl := newFakeGitlab(t)

	impl := gitlabImpl(t, gl.URL, "1", "main", "TOKEN")
	run := buildRun(t, "tok", nil, impl, fullLinks())

	h := NewHandler(Config{BaseConfig: config.BaseConfig{Uuid: testUuid}}, HandlerDeps{Reporters: factoryFor(srv), HTTP: httpClient(), Log: testLog()})
	require.NoError(t, h.Execute(context.Background(), run))

	patches := srv.Patches()
	require.Len(t, patches, 2, "the would-be IN_PROGRESS handover PATCH, then the ABORTED follow-up")
	first := decodePatch(t, patches[0].Body)
	require.Equal(t, "IN_PROGRESS", first.Status, "the handover is still sent")

	last := decodePatch(t, patches[len(patches)-1].Body)
	require.Equal(t, "ABORTED", last.Status, "but the FINAL reported state is ABORTED, not a stale IN_PROGRESS")
	require.Len(t, last.Steps, 1)
	require.Equal(t, StepId, last.Steps[0].Id)
	require.Equal(t, "ABORTED", last.Steps[0].Status)
}

// TestScenario_Gitlab_MissingLinks_OmitsVariablesButStillTriggers pins that a run without
// meshstackBaseUrl/self links omits only those parts; trigger still succeeds.
func TestScenario_Gitlab_MissingLinks_OmitsVariablesButStillTriggers(t *testing.T) {
	srv := meshapitest.NewServer(t)
	gl := newFakeGitlab(t)

	impl := gitlabImpl(t, gl.URL, "42", "main", "TOKEN")
	links := meshapi.LinksDTO{
		RegisterSource: meshapi.LinkDTO{Href: "http://mesh.example/register"},
		UpdateSource:   meshapi.LinkDTO{Href: "http://mesh.example/update"},
		// Self and MeshstackBaseUrl deliberately absent.
	}
	run := buildRun(t, "tok", nil, impl, links)

	h := NewHandler(Config{BaseConfig: config.BaseConfig{Uuid: testUuid}}, HandlerDeps{Reporters: factoryFor(srv), HTTP: httpClient(), Log: testLog()})
	require.NoError(t, h.Execute(context.Background(), run))

	require.Empty(t, gl.formValue("variables[MESHSTACK_SELF_URL]"))
	require.Empty(t, gl.formValue("variables[MESHSTACK_BASE_URL]"))
	require.Equal(t, "http://mesh.example/register", gl.formValue("variables[MESHSTACK_REGISTER_SOURCE_URL]"))
	require.Equal(t, "http://mesh.example/update", gl.formValue("variables[MESHSTACK_UPDATE_SOURCE_URL]"))
	require.Len(t, srv.Patches(), 1) // trigger still succeeded -> handover
}

// TestScenario_Gitlab_TriggerFails_404 pins that a 404 from GitLab -> row-B FAILED update
// with the frozen message shape.
func TestScenario_Gitlab_TriggerFails_404(t *testing.T) {
	srv := meshapitest.NewServer(t)
	gl := newFakeGitlab(t)
	gl.status, gl.body = 404, "not found"

	impl := gitlabImpl(t, gl.URL, "999", "main", "TOKEN")
	run := buildRun(t, "tok", nil, impl, fullLinks())

	h := NewHandler(Config{BaseConfig: config.BaseConfig{Uuid: testUuid}}, HandlerDeps{Reporters: factoryFor(srv), HTTP: httpClient(), Log: testLog()})
	require.NoError(t, h.Execute(context.Background(), run))

	patches := srv.Patches()
	require.Len(t, patches, 1)
	upd := decodePatch(t, patches[0].Body)
	require.Equal(t, "FAILED", upd.Status)
	require.Equal(t, "FAILED", upd.Steps[0].Status)
	require.Equal(t, userMessageTriggerFailed, upd.Steps[0].UserMessage)
	require.Equal(t, "GitLab responded with status: 404 and body: not found", upd.Steps[0].SystemMessage)
}

// TestScenario_Gitlab_TriggerFails_IdentityVerification_And_Generic pins that both the
// identity-verification body and a generic error body produce the SAME update shape as
// the 404 case, modulo status/body -- the classification differs only in logs.
func TestScenario_Gitlab_TriggerFails_IdentityVerification_And_Generic(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"identity verification (403)", 403, `{"message":{"base":["Identity verification is required in order to run CI jobs"]}}`},
		{"generic error (400)", 400, `{"message":{"base":["something else"]}}`},
		{"undeserializable body (html 500)", 500, `<html>oops</html>`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := meshapitest.NewServer(t)
			gl := newFakeGitlab(t)
			gl.status, gl.body = c.status, c.body

			impl := gitlabImpl(t, gl.URL, "1", "main", "TOKEN")
			run := buildRun(t, "tok", nil, impl, fullLinks())

			h := NewHandler(Config{BaseConfig: config.BaseConfig{Uuid: testUuid}}, HandlerDeps{Reporters: factoryFor(srv), HTTP: httpClient(), Log: testLog()})
			require.NoError(t, h.Execute(context.Background(), run))

			patches := srv.Patches()
			require.Len(t, patches, 1)
			upd := decodePatch(t, patches[0].Body)
			require.Equal(t, "FAILED", upd.Status)
			require.Equal(t, "FAILED", upd.Steps[0].Status)
			require.Equal(t, userMessageTriggerFailed, upd.Steps[0].UserMessage)
			require.Contains(t, upd.Steps[0].SystemMessage, c.body)
		})
	}
}

// TestScenario_Gitlab_TriggerFails_Redirect pins that a 3xx response is never followed;
// it is classified as row-B FAILED with the redirect status code, not a second request.
func TestScenario_Gitlab_TriggerFails_Redirect(t *testing.T) {
	srv := meshapitest.NewServer(t)
	gl := newFakeGitlab(t)
	gl.status, gl.body = 302, "redirecting"

	impl := gitlabImpl(t, gl.URL, "1", "main", "TOKEN")
	run := buildRun(t, "tok", nil, impl, fullLinks())

	h := NewHandler(Config{BaseConfig: config.BaseConfig{Uuid: testUuid}}, HandlerDeps{Reporters: factoryFor(srv), HTTP: httpClient(), Log: testLog()})
	require.NoError(t, h.Execute(context.Background(), run))

	require.Equal(t, 1, gl.calls, "the redirect must not be followed")
	upd := decodePatch(t, srv.Patches()[0].Body)
	require.Contains(t, upd.Steps[0].SystemMessage, "302")
}

// TestScenario_Gitlab_TriggerFails_RedirectNeverReachesSecondServer pins WithNoRedirect:
// the multipart body carries the trigger token, so a followed 307 would resend it to
// whatever server the Location header names. It must never be reached; the trigger fails
// cleanly instead (row-B, classified on the 307 itself).
func TestScenario_Gitlab_TriggerFails_RedirectNeverReachesSecondServer(t *testing.T) {
	var secondServerHit atomic.Bool
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		secondServerHit.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer second.Close()

	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, second.URL, http.StatusTemporaryRedirect)
	}))
	defer first.Close()

	srv := meshapitest.NewServer(t)
	impl := gitlabImpl(t, first.URL, "1", "main", "TOKEN")
	run := buildRun(t, "tok", nil, impl, fullLinks())

	h := NewHandler(Config{BaseConfig: config.BaseConfig{Uuid: testUuid}}, HandlerDeps{Reporters: factoryFor(srv), HTTP: httpClient(), Log: testLog()})
	require.NoError(t, h.Execute(context.Background(), run))

	require.False(t, secondServerHit.Load(), "the redirect target must never receive the trigger token")
	upd := decodePatch(t, srv.Patches()[0].Body)
	require.Equal(t, "FAILED", upd.Status)
	require.Contains(t, upd.Steps[0].SystemMessage, "307")
}

// TestScenario_Gitlab_TriggerFails_NeverRetried pins that the trigger POST is not in the
// shared client's retry whitelist: even a retry-capable transport that would otherwise
// retry a transport-retryable 503 leaves the trigger call as exactly one POST, and the
// trigger fails hard instead of silently double-triggering the pipeline.
func TestScenario_Gitlab_TriggerFails_NeverRetried(t *testing.T) {
	var calls atomic.Int32
	gl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer gl.Close()

	retryClient := &http.Client{
		Transport: httpclient.NewRetryTransport(nil, httpclient.RetryOptions{
			MaxRetries:       2,
			Backoff:          httpclient.ExponentialBackoff{MinWait: time.Millisecond, MaxWait: 2 * time.Millisecond},
			WhitelistedPosts: nil,
		}, nil),
	}

	srv := meshapitest.NewServer(t)
	impl := gitlabImpl(t, gl.URL, "1", "main", "TOKEN")
	run := buildRun(t, "tok", nil, impl, fullLinks())

	h := NewHandler(Config{BaseConfig: config.BaseConfig{Uuid: testUuid}}, HandlerDeps{Reporters: factoryFor(srv), HTTP: retryClient, Log: testLog()})
	require.NoError(t, h.Execute(context.Background(), run))

	require.Equal(t, int32(1), calls.Load(), "the trigger POST must never be retried")
	upd := decodePatch(t, srv.Patches()[0].Body)
	require.Equal(t, "FAILED", upd.Status)
	require.Contains(t, upd.Steps[0].SystemMessage, "503")
}

// TestScenario_Gitlab_WrongImplType_And_BlankBaseUrl pin that an internal
// error before ever reaching GitLab (wrong impl type, blank base URL) reports row-C FAILED
// after register already happened.
func TestScenario_Gitlab_WrongImplType_And_BlankBaseUrl(t *testing.T) {
	t.Run("wrong impl type", func(t *testing.T) {
		srv := meshapitest.NewServer(t)
		wrongImpl, err := json.Marshal(meshapi.TerraformImplementation{Type: "TERRAFORM"})
		require.NoError(t, err)
		run := buildRun(t, "tok", nil, wrongImpl, fullLinks())

		h := NewHandler(Config{BaseConfig: config.BaseConfig{Uuid: testUuid}}, HandlerDeps{Reporters: factoryFor(srv), HTTP: httpClient(), Log: testLog()})
		require.NoError(t, h.Execute(context.Background(), run))

		require.Len(t, srv.Registers(), 1, "register happens before the impl-type check")
		upd := decodePatch(t, srv.Patches()[0].Body)
		require.Equal(t, "FAILED", upd.Status)
		require.Contains(t, upd.Steps[0].SystemMessage, "was not of expected type")
	})

	t.Run("blank base url", func(t *testing.T) {
		srv := meshapitest.NewServer(t)
		impl := gitlabImpl(t, "   ", "1", "main", "TOKEN")
		run := buildRun(t, "tok", nil, impl, fullLinks())

		h := NewHandler(Config{BaseConfig: config.BaseConfig{Uuid: testUuid}}, HandlerDeps{Reporters: factoryFor(srv), HTTP: httpClient(), Log: testLog()})
		require.NoError(t, h.Execute(context.Background(), run))

		upd := decodePatch(t, srv.Patches()[0].Body)
		require.Equal(t, "FAILED", upd.Status)
		require.Contains(t, upd.Steps[0].SystemMessage, "URL should not be empty")
	})
}

// TestScenario_Gitlab_EmptyTriggerToken pins that an empty PipelineTriggerToken is sent
// as-is (no decrypt step to fail on it), so the request still carries an empty (present)
// token field.
func TestScenario_Gitlab_EmptyTriggerToken(t *testing.T) {
	srv := meshapitest.NewServer(t)
	gl := newFakeGitlab(t)
	impl := gitlabImpl(t, gl.URL, "1", "main", "")
	run := buildRun(t, "tok", nil, impl, fullLinks())

	h := NewHandler(Config{BaseConfig: config.BaseConfig{Uuid: testUuid}}, HandlerDeps{Reporters: factoryFor(srv), HTTP: httpClient(), Log: testLog()})
	require.NoError(t, h.Execute(context.Background(), run))

	require.Equal(t, 1, gl.calls)
	require.Contains(t, gl.lastForm, "token")
	require.Empty(t, gl.formValue("token"))
	require.Len(t, srv.Patches(), 1) // handover still reported
}

// TestScenario_Gitlab_ImplSecret_StrippedFromHandoverPayload checks the secret hygiene
// at wire level: the trigger token must NOT appear anywhere in the MESHSTACK_RUN payload
// -- meshapi.SanitizeRunObjectForHandover reduces the implementation object to just its
// `type` field, so the pipelineTriggerToken it carries never reaches the pipeline via
// MESHSTACK_RUN, only via the separate "token" form field. A (by-then-already-decrypted)
// sensitive input's value passes through into MESHSTACK_RUN unchanged -- inputs are
// forwarded by design, decryption happens upstream at the dispatch boundary.
func TestScenario_Gitlab_ImplSecret_StrippedFromHandoverPayload(t *testing.T) {
	srv := meshapitest.NewServer(t)
	gl := newFakeGitlab(t)

	inputs := []meshapi.BuildingBlockInputSpecDTO{
		{Key: "secret", Value: "plaintext-secret-input", Type: "STRING", IsSensitive: true},
	}
	impl := gitlabImpl(t, gl.URL, "1", "main", "plaintext-trigger-token")
	run := buildRun(t, "tok", inputs, impl, fullLinks())

	h := NewHandler(Config{BaseConfig: config.BaseConfig{Uuid: testUuid}}, HandlerDeps{Reporters: factoryFor(srv), HTTP: httpClient(), Log: testLog()})
	require.NoError(t, h.Execute(context.Background(), run))

	require.Equal(t, "plaintext-trigger-token", gl.formValue("token"), "the token FIELD carries the trigger token")

	run_ := gl.formValue("variables[MESHSTACK_RUN]")
	require.Contains(t, run_, "plaintext-secret-input", "the sensitive input's value passes through into MESHSTACK_RUN")
	require.NotContains(t, run_, "plaintext-trigger-token", "the leak pin: MESHSTACK_RUN must never carry the implementation's trigger token")
}

// TestScenario_Gitlab_SingleRun_FileSource is the single-run twin: a pre-decrypted run file
// (as the k8s controller hands it over) still triggers correctly and exits 0. MESHSTACK_RUN
// is impl-stripped like every other mode, so the trigger token does not appear there even
// though the mounted file already carries it in plaintext.
func TestScenario_Gitlab_SingleRun_FileSource(t *testing.T) {
	gl := newFakeGitlab(t)
	srv := meshapitest.NewServer(t)

	impl := gitlabImpl(t, gl.URL, "7", "main", "already-plaintext-token")
	run := buildRun(t, "run-token-xyz", nil, impl, fullLinks())

	raw, err := base64.StdEncoding.DecodeString(run.RawJson)
	require.NoError(t, err)
	runFile := t.TempDir() + "/run.json"
	require.NoError(t, os.WriteFile(runFile, raw, 0o600))
	t.Setenv(envRunJsonFilePath, runFile)

	cfg := Config{BaseConfig: config.BaseConfig{Uuid: testUuid, Api: config.Api{Url: srv.URL}}}
	code := runSingleRunForTest(context.Background(), testLog(), cfg, meshapi.Identity{Name: "gitlab-block-runner"})
	require.Equal(t, 0, code)

	require.Equal(t, "already-plaintext-token", gl.formValue("token"))
	require.NotContains(t, gl.formValue("variables[MESHSTACK_RUN]"), "already-plaintext-token",
		"MESHSTACK_RUN is impl-stripped even in single-run mode")
	require.Len(t, srv.Patches(), 1)
	upd := decodePatch(t, srv.Patches()[0].Body)
	require.Equal(t, "IN_PROGRESS", upd.Status)
}

// TestScenario_Gitlab_SingleRun_ExitCodes pins that a report/update-transport failure
// exits non-zero (Kotlin exit-1 parity); a missing run file also exits non-zero -- the
// sanctioned tightening of Kotlin's exit-0 swallow.
func TestScenario_Gitlab_SingleRun_ExitCodes(t *testing.T) {
	t.Run("missing run file", func(t *testing.T) {
		t.Setenv(envRunJsonFilePath, t.TempDir()+"/does-not-exist.json")
		cfg := Config{BaseConfig: config.BaseConfig{Uuid: testUuid, Api: config.Api{Url: "http://unused.invalid"}}}
		require.Equal(t, 1, runSingleRunForTest(context.Background(), testLog(), cfg, meshapi.Identity{}))
	})

	t.Run("update transport failure", func(t *testing.T) {
		gl := newFakeGitlab(t)
		srv := meshapitest.NewServer(t)
		srv.SeedPatchResponse(meshapitest.PatchResponse{Status: 500})

		impl := gitlabImpl(t, gl.URL, "7", "main", "tok")
		run := buildRun(t, "run-token-xyz", nil, impl, fullLinks())
		raw, err := base64.StdEncoding.DecodeString(run.RawJson)
		require.NoError(t, err)
		runFile := t.TempDir() + "/run.json"
		require.NoError(t, os.WriteFile(runFile, raw, 0o600))
		t.Setenv(envRunJsonFilePath, runFile)

		cfg := Config{BaseConfig: config.BaseConfig{Uuid: testUuid, Api: config.Api{Url: srv.URL}}}
		require.Equal(t, 1, runSingleRunForTest(context.Background(), testLog(), cfg, meshapi.Identity{}))
	})
}

// TestScenario_Gitlab_CancelledDuringTrigger_ReportsAborted pins the shutdown-abort contract for
// gitlab's one in-flight window: if the run's ctx is cancelled (shutdown grace expiry, or a standalone
// k8s Job receiving SIGTERM) while the trigger POST is in flight, the run must report terminal
// ABORTED — not the FAILED a bare transport error would otherwise produce via failInternal.
func TestScenario_Gitlab_CancelledDuringTrigger_ReportsAborted(t *testing.T) {
	srv := meshapitest.NewServer(t)
	gl := newFakeGitlab(t)

	impl := gitlabImpl(t, gl.URL, "1", "main", "TOKEN")
	run := buildRun(t, "tok", nil, impl, fullLinks())

	// Cancelled before the trigger: triggerPipeline's request fails with ctx.Canceled, exercising
	// the ctx.Err() branch in Execute without depending on race-y mid-flight timing.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	h := NewHandler(Config{BaseConfig: config.BaseConfig{Uuid: testUuid}}, HandlerDeps{Reporters: factoryFor(srv), HTTP: httpClient(), Log: testLog()})
	require.NoError(t, h.Execute(ctx, run))

	patches := srv.Patches()
	require.NotEmpty(t, patches, "a cancelled trigger must still report a terminal status")
	last := decodePatch(t, patches[len(patches)-1].Body)
	require.Equal(t, "ABORTED", last.Status, "a ctx-cancel during the trigger must report ABORTED, not FAILED")
	for _, p := range patches {
		require.NotEqual(t, "FAILED", decodePatch(t, p.Body).Status, "cancellation must never surface as FAILED")
	}
}
