package tf

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/meshcloud/building-block-runner/internal/build"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/report"
)

// runApiAuth implements meshapi.AuthProvider with Bearer token priority over a fallback AuthProvider.
// The runToken is fixed at construction (NewRunApi): every RunApi is now run-scoped -- one
// instance per claimed run (dispatch.RunHandler, single-run mode), never reused across runs -- so
// there is no longer a mutable set/clear protocol to race or reset between runs.
type runApiAuth struct {
	runToken *string
	baseAuth meshapi.AuthProvider
}

func (a *runApiAuth) AuthHeader() string {
	if a.runToken != nil && *a.runToken != "" {
		slog.Info("using Bearer token for run-specific operations", "component", "runApi")
		return "Bearer " + *a.runToken
	}
	if a.baseAuth != nil {
		slog.Info("using configured auth for API requests", "component", "runApi")
		return a.baseAuth.AuthHeader()
	}
	return ""
}

type RunApiClient struct {
	rid        string
	baseURL    string
	auth       *runApiAuth
	client     *meshapi.Client
	httpClient *http.Client
}

const (
	EP_State = "%s/api/terraform/state/workspace/%s/buildingBlock/%s"
)

// RunApi is tf's run-scoped status backchannel plus the predecessor-plan artifact download.
// It IS a report.Reporter (Register + diffed-step Report): report.Observer drives the 10s ticker
// through it, and each Report renders the tf RunStatusUpdateDTO wire shape via report.ToStatusUpdate
// (tf keeps its full-output DTO, unlike the lean SourceUpdateDTO the other ports use).
type RunApi interface {
	report.Reporter
	// DownloadPredecessorArtifact streams the bytes referenced by the given absolute URL
	// (the runner-facing _links.planArtifact.href) into w using the current run authentication.
	DownloadPredecessorArtifact(url string, w io.Writer) error
}

// NewRunApi builds a run-scoped RunApi from the runner's API backend config and uuid, threaded
// explicitly in place of the former AppConfig global reads. runToken authenticates every
// request this instance sends ("" falls back to apiBackend's configured auth, e.g. single-run
// mode's no-op basic auth waiver); callers needing a different run's token construct a fresh
// RunApi rather than mutating this one (no shared, reusable token slot).
func NewRunApi(apiBackend RunApiConfig, runnerUuid string, runToken string) RunApi {
	auth := &runApiAuth{
		runToken: &runToken,
		baseAuth: apiBackend.NewAuthProvider(),
	}

	identity := meshapi.Identity{Name: "tf-block-runner", Version: build.Version}
	httpClient := &http.Client{}
	return &RunApiClient{
		rid:        runnerUuid,
		baseURL:    apiBackend.Url,
		auth:       auth,
		client:     meshapi.NewClient(apiBackend.Url, runnerUuid, auth, meshapi.WithIdentity(identity)),
		httpClient: httpClient,
	}
}

func (api *RunApiClient) DownloadPredecessorArtifact(url string, w io.Writer) error {
	return api.client.DownloadArtifact(url, w)
}

func (api *RunApiClient) Register(runStatus report.RunStatus) error {
	steps := make([]meshapi.StepRegistrationDTO, 0)
	for _, s := range runStatus.Steps {
		steps = append(steps, meshapi.StepRegistrationDTO{
			Id:          s.Name,
			DisplayName: s.DisplayName,
			Status:      nil,
		})
	}

	registration := meshapi.RegistrationDTO{
		Source: meshapi.SourceDTO{
			Id: api.rid,
		},
		Steps: steps,
	}

	if err := api.client.RegisterSource(runStatus.RunId, registration); err != nil {
		return err
	}

	// 409 Conflict is handled inside the shared client (treated as success).
	slog.Info("registered source for run", "component", "runApi", "runId", runStatus.RunId)
	return nil
}

// Report PATCHes the status update the observer hands it (the changed/new steps only — the diffing
// lives in report.Observer). The wire body is the tf-specific RunStatusUpdateDTO built by
// report.ToStatusUpdate, so the on-wire shape is unchanged; only the step SET per PATCH shrinks to
// what changed since the last send.
func (api *RunApiClient) Report(status report.RunStatus) (bool, error) {
	dto, err := report.ToStatusUpdate(status, api.rid, meshapi.RunTypeTerraform)
	if err != nil {
		return false, err
	}

	data, err := api.client.PatchStatus(status.RunId, api.rid, dto)
	if err != nil {
		return false, err
	}

	var runUpdateResponse meshapi.RunUpdateResponseDTO
	if err := json.Unmarshal(data, &runUpdateResponse); err != nil {
		return false, err
	}

	return runUpdateResponse.Abort, nil
}
