package tfrun

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
)

// runApiAuth implements meshapi.AuthProvider with Bearer token priority over Basic auth.
// The runToken pointer is updated by SetRunToken / ClearRunToken so the shared meshapi.Client
// automatically picks up the latest auth value on every request.
type runApiAuth struct {
	runToken *string
	basic    string
}

func (a *runApiAuth) AuthHeader() string {
	if a.runToken != nil && *a.runToken != "" {
		log.Printf("[AUTH] Using Bearer token for run-specific operations")
		return "Bearer " + *a.runToken
	}
	if a.basic != "" {
		log.Printf("[AUTH] Using Basic auth for API requests")
		return a.basic
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

type RunApi interface {
	FetchRunDetails(nodePostfix string) (*Run, error)
	UpdateState(status *RunStatus) (bool, error)
	Register(status *RunStatus) error
	SetRunToken(token string) // Set the runToken from the fetched run
	ClearRunToken()           // Clear the runToken to force basic auth for next fetch
}

func NewRunApi() RunApi {
	var basicAuth string
	if AppConfig.RunApiBackend.User != "" && AppConfig.RunApiBackend.Password != "" {
		basicAuth = meshapi.BasicAuth{
			Username: AppConfig.RunApiBackend.User,
			Password: AppConfig.RunApiBackend.Password,
		}.AuthHeader()
	}

	auth := &runApiAuth{
		runToken: nil,
		basic:    basicAuth,
	}

	httpClient := &http.Client{}
	return &RunApiClient{
		rid:        AppConfig.RunnerUuid,
		baseURL:    AppConfig.RunApiBackend.Url,
		auth:       auth,
		client:     meshapi.NewClient(AppConfig.RunApiBackend.Url, AppConfig.RunnerUuid, auth),
		httpClient: httpClient,
	}
}

// SetRunToken sets the runToken from the fetched run for subsequent API calls
func (api *RunApiClient) SetRunToken(token string) {
	api.auth.runToken = &token
}

// ClearRunToken clears the runToken to force basic auth for the next fetch.
func (api *RunApiClient) ClearRunToken() {
	api.auth.runToken = nil
}

func (api *RunApiClient) FetchRunDetails(nodePostfix string) (*Run, error) {
	requester := fmt.Sprintf("%s-%s", api.rid, nodePostfix)

	// Use a client with the current auth but override the node ID
	client := meshapi.NewClientWithHTTP(api.baseURL, requester, api.auth, api.httpClient)

	dto, _, err := client.FetchRun(AppConfig.RunnerUuid)
	if err != nil {
		return nil, err
	}

	// Extract and store the runToken for subsequent API calls
	api.SetRunToken(dto.Spec.RunToken)

	return runDTOToInternal(dto)
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
	log.Printf("[RUNNER] Registered source for run %s", runStatus.RunId)
	return nil
}

func (api *RunApiClient) UpdateState(status *RunStatus) (bool, error) {
	dto, err := status.toExternal()
	if err != nil {
		return false, err
	}

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

