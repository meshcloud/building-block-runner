package tf

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/meshcloud/building-block-runner/internal/build"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

// runApiAuth implements meshapi.AuthProvider with Bearer token priority over a fallback AuthProvider.
// The runToken pointer is updated by SetRunToken / ClearRunToken so the shared meshapi.Client
// automatically picks up the latest auth value on every request.
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
	// identity is stamped on the User-Agent + X-Meshcloud-Runner-* headers. NewRunApi sets
	// it to {"tf-block-runner", build.Version} (replacing the former global
	// meshapi.SetClientMetadata call); the zero value in tests reproduces the historic
	// "unknown-runner"/"dev" defaults.
	identity meshapi.Identity
	// dec decrypts sensitive inputs + SSH key while mapping a fetched run (polling mode).
	dec Decryptor
}

const (
	EP_State = "%s/api/terraform/state/workspace/%s/buildingBlock/%s"
)

type RunApi interface {
	FetchRunDetails(nodePostfix string) (*Run, error)
	UpdateState(status *RunStatus) (bool, error)
	Register(status *RunStatus) error
	SetRunToken(token string) // Set the runToken from the fetched run
	ClearRunToken()           // Clear the runToken to force basic auth for next fetch
	// DownloadPredecessorArtifact streams the bytes referenced by the given absolute URL
	// (the runner-facing _links.planArtifact.href) into w using the current run authentication.
	DownloadPredecessorArtifact(url string, w io.Writer) error
}

func NewRunApi(dec Decryptor) RunApi {
	auth := &runApiAuth{
		runToken: nil,
		baseAuth: AppConfig.RunApiBackend.NewAuthProvider(),
	}

	identity := meshapi.Identity{Name: "tf-block-runner", Version: build.Version}
	httpClient := &http.Client{}
	return &RunApiClient{
		rid:        AppConfig.RunnerUuid,
		baseURL:    AppConfig.RunApiBackend.Url,
		auth:       auth,
		identity:   identity,
		client:     meshapi.NewClient(AppConfig.RunApiBackend.Url, AppConfig.RunnerUuid, auth, meshapi.WithIdentity(identity)),
		httpClient: httpClient,
		dec:        dec,
	}
}

// SetRunToken sets the runToken from the fetched run for subsequent API calls.
func (api *RunApiClient) SetRunToken(token string) {
	api.auth.runToken = &token
}

// ClearRunToken clears the runToken to force basic auth for the next fetch.
func (api *RunApiClient) ClearRunToken() {
	api.auth.runToken = nil
}

func (api *RunApiClient) DownloadPredecessorArtifact(url string, w io.Writer) error {
	return api.client.DownloadArtifact(url, w)
}

func (api *RunApiClient) FetchRunDetails(nodePostfix string) (*Run, error) {
	requester := fmt.Sprintf("%s-%s", api.rid, nodePostfix)

	// Use a client with the current auth but override the node ID (identity headers stay
	// identical to api.client's, so the per-fetch override is invisible on the wire).
	client := meshapi.NewClientWithHTTP(api.baseURL, requester, api.auth, api.httpClient, meshapi.WithIdentity(api.identity))

	dto, _, err := client.FetchRun(AppConfig.RunnerUuid)
	if err != nil {
		return nil, err
	}

	// Extract and store the runToken for subsequent API calls
	api.SetRunToken(dto.Spec.RunToken)

	return runDTOToInternal(dto, api.dec)
}

func (api *RunApiClient) Register(runStatus *RunStatus) error {
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
			Id: AppConfig.RunnerUuid,
		},
		Steps: steps,
	}

	if err := api.client.RegisterSource(runStatus.RunId, registration); err != nil {
		return err
	}

	// 409 Conflict is handled inside the shared client (treated as success).
	slog.Info("registered source for run", "component", "runApi", "run", runStatus.RunId)
	return nil
}

func (api *RunApiClient) UpdateState(status *RunStatus) (bool, error) {
	dto := status.toExternal()

	data, err := api.client.PatchStatus(status.RunId, AppConfig.RunnerUuid, dto)
	if err != nil {
		return false, err
	}

	var runUpdateResponse meshapi.RunUpdateResponseDTO
	if err := json.Unmarshal(data, &runUpdateResponse); err != nil {
		return false, err
	}

	return runUpdateResponse.Abort, nil
}
