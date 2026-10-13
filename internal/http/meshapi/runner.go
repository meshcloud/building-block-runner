package meshapi

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	gohttp "net/http"
	"net/url"
	"strings"
	"time"
	"uuid"

	"github.com/meshcloud/building-block-runner/internal/http"
)

type MeshRunnerRun struct {
	ApiVersion string                `json:"apiVersion"`
	Kind       string                `json:"kind"`
	Metadata   MeshRunnerRunMetadata `json:"metadata"`
	Spec       MeshRunnerRunSpec     `json:"spec"`
	Status     string                `json:"status"`
	Links      MeshRunnerRunLinks    `json:"_links"`
}

type MeshRunnerRunMetadata struct {
	Uuid uuid.UUID `json:"uuid"`
}

type MeshRunnerRunSpec struct {
	RunNumber     int                        `json:"runNumber"`
	Behavior      string                     `json:"behavior"`
	BuildingBlock MeshRunnerRunBuildingBlock `json:"buildingBlock"`
	Definition    MeshRunnerRunDefinition    `json:"buildingBlockDefinition"`
	RunToken      string                     `json:"runToken"`
	Trigger       *MeshRunnerRunTrigger      `json:"trigger,omitzero"`
}

type MeshRunnerRunBuildingBlock struct {
	Uuid string                         `json:"uuid"`
	Spec MeshRunnerRunBuildingBlockSpec `json:"spec"`
}

type MeshRunnerRunBuildingBlockSpec struct {
	DisplayName            string                     `json:"displayName"`
	WorkspaceIdentifier    string                     `json:"workspaceIdentifier"`
	ProjectIdentifier      string                     `json:"projectIdentifier,omitempty"`
	FullPlatformIdentifier string                     `json:"fullPlatformIdentifier,omitempty"`
	Inputs                 []MeshRunnerRunInput       `json:"inputs"`
	ParentBuildingBlocks   []MeshRunnerRunParentBlock `json:"parentBuildingBlocks"`
}

type MeshRunnerRunParentBlock struct {
	BuildingBlockUuid string `json:"buildingBlockUuid"`
	DefinitionUuid    string `json:"definitionUuid"`
}

type MeshRunnerRunInput struct {
	Key string `json:"key"`
	// Value stays raw JSON, so that an INTEGER beyond float64 or int64 keeps every digit.
	Value       jsontext.Value `json:"value"`
	Type        string         `json:"type"`
	IsSensitive bool           `json:"isSensitive"`
	Env         bool           `json:"isEnvironment"`
}

type MeshRunnerRunDefinition struct {
	Uuid string                      `json:"uuid"`
	Spec MeshRunnerRunDefinitionSpec `json:"spec"`
}

type MeshRunnerRunDefinitionSpec struct {
	WorkspaceIdentifier string         `json:"workspaceIdentifier"`
	Version             int            `json:"version"`
	Implementation      jsontext.Value `json:"implementation"`
}

func (d MeshRunnerRunDefinitionSpec) ImplementationType() (MeshRunnerImplementationType, error) {
	var implementation struct {
		Type MeshRunnerImplementationType `json:"type"`
	}
	if err := json.Unmarshal(d.Implementation, &implementation); err != nil {
		return "", fmt.Errorf("cannot read the implementation type: %w", err)
	}
	return implementation.Type, nil
}

type MeshRunnerRunTrigger struct {
	Reason string              `json:"reason"`
	Author MeshRunnerRunAuthor `json:"author"`
}

type MeshRunnerRunAuthor struct {
	Identifier  string  `json:"identifier"`
	Type        string  `json:"type"`
	DisplayName *string `json:"displayName"`
	Username    *string `json:"username"`
	Email       *string `json:"email"`
	Euid        *string `json:"euid"`
}

type MeshRunnerRunLinks struct {
	Self             MeshRunnerRunLink `json:"self"`
	RegisterSource   MeshRunnerRunLink `json:"registerSource"`
	UpdateSource     MeshRunnerRunLink `json:"updateSource"`
	MeshstackBaseUrl MeshRunnerRunLink `json:"meshstackBaseUrl"`
	// PlanArtifact is set only when an APPLY run applies the plan of its DETECT run.
	PlanArtifact   MeshRunnerRunLink `json:"planArtifact,omitzero"`
	ArtifactUpload MeshRunnerRunLink `json:"artifactUpload,omitzero"`
	// TfStateLock is empty on a meshStack that cannot lock the state.
	TfStateLock MeshRunnerRunLink `json:"tfStateLock,omitzero"`
}

type MeshRunnerRunLink struct {
	Href      string `json:"href"`
	Templated bool   `json:"templated,omitzero"`
}

func (l MeshRunnerRunLink) parse(name string) (*url.URL, error) {
	if l.Href == "" {
		return nil, fmt.Errorf("the run has no %s link", name)
	}
	return url.Parse(l.Href)
}

type MeshRunnerImplementationType string

const (
	MeshRunnerImplementationTypeManual         MeshRunnerImplementationType = "MANUAL"
	MeshRunnerImplementationTypeTerraform      MeshRunnerImplementationType = "TERRAFORM"
	MeshRunnerImplementationTypeGithubWorkflow MeshRunnerImplementationType = "GITHUB_WORKFLOW"
	MeshRunnerImplementationTypeGitlabCiCd     MeshRunnerImplementationType = "GITLAB_CICD"
	MeshRunnerImplementationTypeAzureDevops    MeshRunnerImplementationType = "AZURE_DEVOPS"
)

type MeshRunnerTerraformImplementation struct {
	Type                       string               `json:"type"`
	TerraformVersion           string               `json:"terraformVersion"`
	RepositoryUrl              string               `json:"repositoryUrl"`
	RepositoryPath             *string              `json:"repositoryPath,omitzero"`
	RefName                    *string              `json:"refName,omitzero"`
	SshPrivateKey              *string              `json:"sshPrivateKey,omitzero"`
	KnownHost                  *MeshRunnerKnownHost `json:"knownHost,omitzero"`
	Async                      bool                 `json:"async"`
	UseMeshHttpBackendFallback bool                 `json:"useMeshHttpBackendFallback"`
	PreRunScript               *string              `json:"preRunScript,omitzero"`
}

type MeshRunnerKnownHost struct {
	Host     string `json:"host"`
	KeyType  string `json:"keyType"`
	KeyValue string `json:"keyValue"`
}

type MeshRunnerGithubImplementation struct {
	Type               string  `json:"type"`
	GithubBaseUrl      string  `json:"githubBaseUrl"`
	Owner              string  `json:"owner"`
	AppId              string  `json:"appId"`
	AppPem             string  `json:"appPem"`
	Repository         string  `json:"repository"`
	Branch             string  `json:"branch"`
	ApplyWorkflow      string  `json:"applyWorkflow"`
	DestroyWorkflow    *string `json:"destroyWorkflow,omitzero"`
	Async              bool    `json:"async"`
	OmitRunObjectInput bool    `json:"omitRunObjectInput"`
}

type MeshRunnerGitlabImplementation struct {
	Type                 string `json:"type"`
	GitlabBaseUrl        string `json:"gitlabBaseUrl"`
	ProjectId            string `json:"projectId"`
	RefName              string `json:"refName"`
	PipelineTriggerToken string `json:"pipelineTriggerToken"`
}

type MeshRunnerAzureDevopsImplementation struct {
	Type                string  `json:"type"`
	AzureDevOpsBaseUrl  string  `json:"azureDevOpsBaseUrl"`
	Organization        string  `json:"organization"`
	Project             string  `json:"project"`
	PipelineId          string  `json:"pipelineId"`
	PersonalAccessToken string  `json:"personalAccessToken"`
	Async               bool    `json:"async"`
	RefName             *string `json:"refName,omitzero"`
}

type MeshRunnerRunSourceRegistration struct {
	Source MeshRunnerRunSource             `json:"source"`
	Steps  []MeshRunnerRunStepRegistration `json:"steps"`
}

type MeshRunnerRunSource struct {
	Id          uuid.UUID `json:"id"`
	ExternalId  *string   `json:"externalId,omitzero"`
	ExternalUrl *string   `json:"externalUrl,omitzero"`
}

type MeshRunnerRunStepRegistration struct {
	Id          string  `json:"id"`
	DisplayName string  `json:"displayName"`
	Status      *string `json:"status"`
}

type MeshRunnerRunStatusUpdate struct {
	BlockRunId      uuid.UUID                       `json:"blockRunId,omitzero"`
	Source          uuid.UUID                       `json:"source,omitzero"`
	Type            MeshRunnerImplementationType    `json:"type,omitempty"`
	Status          *string                         `json:"status"`
	CreatedOn       time.Time                       `json:"createdOn,omitzero"`
	Summary         *string                         `json:"summary"`
	Steps           []MeshRunnerRunStepStatusUpdate `json:"steps"`
	ChangesDetected *bool                           `json:"changesDetected,omitzero"`
}

type MeshRunnerRunStepStatusUpdate struct {
	Id            string                         `json:"id"`
	DisplayName   string                         `json:"displayName"`
	Status        *string                        `json:"status,omitzero"`
	UserMessage   *string                        `json:"userMessage,omitzero"`
	SystemMessage *string                        `json:"systemMessage,omitzero"`
	Outputs       map[string]MeshRunnerRunOutput `json:"outputs,omitempty"`
}

type MeshRunnerRunOutput struct {
	Value     any    `json:"value"`
	Type      string `json:"type"`
	Sensitive bool   `json:"isSensitive"`
}

type MeshRunnerRunStatusUpdateResult struct {
	Abort bool `json:"runAborted"`
}

type MeshRunnerRegistration struct {
	Metadata MeshRunnerRegistrationMetadata `json:"metadata"`
	Spec     MeshRunnerRegistrationSpec     `json:"spec"`
}

type MeshRunnerRegistrationMetadata struct {
	Uuid             uuid.UUID `json:"uuid"`
	OwnedByWorkspace string    `json:"ownedByWorkspace"`
}

type MeshRunnerRegistrationSpec struct {
	DisplayName                string                     `json:"displayName"`
	PublicKey                  string                     `json:"publicKey"`
	ImplementationType         string                     `json:"implementationType"`
	WorkloadIdentityFederation *MeshRunnerRegistrationWif `json:"workloadIdentityFederation,omitzero"`
}

type MeshRunnerRegistrationWif struct {
	Issuer          string                             `json:"issuer"`
	SubjectTemplate string                             `json:"subjectTemplate"`
	Gcp             *MeshRunnerRegistrationWifProvider `json:"gcp,omitzero"`
	Aws             *MeshRunnerRegistrationWifProvider `json:"aws,omitzero"`
	Azure           *MeshRunnerRegistrationWifProvider `json:"azure,omitzero"`
}

type MeshRunnerRegistrationWifProvider struct {
	Audience  string `json:"audience"`
	TokenPath string `json:"tokenPath"`
}

const (
	runMediaType    = "application/vnd.meshcloud.api.meshbuildingblockrun.v1.hal+json"
	runnerMediaType = "application/vnd.meshcloud.api.meshbuildingblockrunner.v1-preview.hal+json"

	maxArtifactBytes = 128 << 20
)

var ErrNoRunToClaim = errors.New("no run to claim")

type MeshRunnerClient interface {
	ClaimRun(ctx context.Context, runnerUuid uuid.UUID) (MeshRunnerRun, error)
	// ForRun authorizes with the run's token, which every request about the run needs.
	ForRun(run MeshRunnerRun) MeshRunnerRunClient
	// UpdateRegistration never creates the runner, because meshStack rejects a client-chosen uuid.
	UpdateRegistration(ctx context.Context, registration MeshRunnerRegistration) error
}

type MeshRunnerRunClient interface {
	RegisterSource(ctx context.Context, registration MeshRunnerRunSourceRegistration) error
	PatchStatus(ctx context.Context, sourceId uuid.UUID, update MeshRunnerRunStatusUpdate) (MeshRunnerRunStatusUpdateResult, error)
	DownloadPlanArtifact(ctx context.Context, w io.Writer) error
	UploadArtifact(ctx context.Context, path string) error
}

func NewMeshRunnerClient(client http.AuthorizedClient, meshStackUrl *url.URL) MeshRunnerClient {
	return meshRunnerClient{runnerHttpClient{client}, meshStackUrl}
}

type meshRunnerClient struct {
	http         runnerHttpClient
	meshStackUrl *url.URL
}

func (c meshRunnerClient) ClaimRun(ctx context.Context, runnerUuid uuid.UUID) (MeshRunnerRun, error) {
	claimUrl := c.meshStackUrl.JoinPath("api/meshobjects/meshbuildingblockruns/create")
	claimUrl.RawQuery = url.Values{"forRunnerUuid": {runnerUuid.String()}}.Encode()
	run, err := c.http.do[MeshRunnerRun](ctx, http.MethodPost, claimUrl, http.WithHeaders(gohttp.Header{
		"Accept":       {runMediaType},
		"Content-Type": {runMediaType},
	}))
	if httpErr, ok := errors.AsType[http.Error](err); ok && httpErr.IsNotFound() {
		return run, ErrNoRunToClaim
	}
	if err == nil && run.Metadata.Uuid == (uuid.UUID{}) {
		err = errors.New("the claimed run has no uuid")
	}
	return run, err
}

func (c meshRunnerClient) ForRun(run MeshRunnerRun) MeshRunnerRunClient {
	runToken := http.BearerToken(run.Spec.RunToken)
	return meshRunnerRunClient{runnerHttpClient{c.http.Client.WithAuthorization(runToken)}, run.Links}
}

func (c meshRunnerClient) UpdateRegistration(ctx context.Context, registration MeshRunnerRegistration) error {
	runner := struct {
		ApiVersion             string `json:"apiVersion"`
		Kind                   string `json:"kind"`
		MeshRunnerRegistration `json:",inline"`
	}{"v1-preview", "meshBuildingBlockRunner", registration}
	runnerUrl := c.meshStackUrl.JoinPath("api/meshobjects/meshbuildingblockrunners", registration.Metadata.Uuid.String())
	_, err := c.http.do[[]byte](ctx, http.MethodPut, runnerUrl, http.WithJsonPayload(runner, runnerMediaType))
	if httpErr, ok := errors.AsType[http.Error](err); ok && httpErr.IsNotFound() {
		return fmt.Errorf("runner %s does not exist in meshStack, create it in the meshStack UI or API first: %w", registration.Metadata.Uuid, err)
	}
	return err
}

type meshRunnerRunClient struct {
	http  runnerHttpClient
	links MeshRunnerRunLinks
}

func (c meshRunnerRunClient) RegisterSource(ctx context.Context, registration MeshRunnerRunSourceRegistration) error {
	registerUrl, err := c.links.RegisterSource.parse("registerSource")
	if err != nil {
		return err
	}
	_, err = c.http.do[[]byte](ctx, http.MethodPost, registerUrl, http.WithJsonPayload(registration, runMediaType))
	if httpErr, ok := errors.AsType[http.Error](err); ok && httpErr.IsConflict() {
		return nil
	}
	return err
}

func (c meshRunnerRunClient) PatchStatus(ctx context.Context, sourceId uuid.UUID, update MeshRunnerRunStatusUpdate) (MeshRunnerRunStatusUpdateResult, error) {
	updateSource := MeshRunnerRunLink{Href: strings.ReplaceAll(c.links.UpdateSource.Href, "{sourceId}", sourceId.String())}
	updateUrl, err := updateSource.parse("updateSource")
	if err != nil {
		return MeshRunnerRunStatusUpdateResult{}, err
	}
	return c.http.do[MeshRunnerRunStatusUpdateResult](ctx, http.MethodPatch, updateUrl, http.WithJsonPayload(update, runMediaType))
}

func (c meshRunnerRunClient) DownloadPlanArtifact(ctx context.Context, w io.Writer) error {
	downloadUrl, err := c.links.PlanArtifact.parse("planArtifact")
	if err != nil {
		return err
	}
	capped := &cappedWriter{w, maxArtifactBytes}
	return c.http.DoStreamingRequest(ctx, http.MethodGet, downloadUrl, capped, c.http.version(), http.WithAccept("application/octet-stream"))
}

func (c meshRunnerRunClient) UploadArtifact(ctx context.Context, path string) error {
	uploadUrl, err := c.links.ArtifactUpload.parse("artifactUpload")
	if err != nil {
		return err
	}
	_, err = c.http.do[[]byte](ctx, http.MethodPut, uploadUrl, http.WithBodyFile(path), http.WithHeaders(gohttp.Header{
		"Accept":       {"*/*"},
		"Content-Type": {"application/octet-stream"},
	}))
	return err
}

type runnerHttpClient struct {
	http.AuthorizedClient
}

func (c runnerHttpClient) do[R any](ctx context.Context, method string, url *url.URL, options ...http.RequestOption) (R, error) {
	return c.DoRequest[R](ctx, method, url, append(options, c.version())...)
}

func (c runnerHttpClient) version() http.RequestOption {
	return http.WithHeaders(gohttp.Header{"X-Meshcloud-Runner-Version": {c.UserAgent.Version}})
}

// cappedWriter keeps a misbehaving server from filling the disk.
type cappedWriter struct {
	io.Writer

	remaining int64
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, fmt.Errorf("the artifact exceeds %d bytes", maxArtifactBytes)
	}
	w.remaining -= int64(len(p))
	return w.Writer.Write(p)
}
