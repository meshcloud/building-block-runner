package tfrun

import (
	"encoding/base64"
	"encoding/json/v2"
	"fmt"

	"github.com/meshcloud/building-block-runner/internal/http/meshapi"
)

func newRun(run meshapi.MeshRunnerRun) (*Run, error) {
	behavior, err := DetermineBehavior(run.Spec.Behavior)
	if err != nil {
		return nil, err
	}

	var impl meshapi.MeshRunnerTerraformImplementation
	if err := json.Unmarshal(run.Spec.Definition.Spec.Implementation, &impl); err != nil {
		return nil, fmt.Errorf("implementation: %w", err)
	}

	vars, err := toVariables(run.Spec.BuildingBlock.Spec.Inputs)
	if err != nil {
		return nil, err
	}

	runJson, err := json.Marshal(run)
	if err != nil {
		return nil, err
	}

	spec := run.Spec.BuildingBlock.Spec
	return &Run{
		Id:                     run.Metadata.Uuid.String(),
		TerraformVersion:       impl.TerraformVersion,
		RunToken:               run.Spec.RunToken,
		MeshstackBaseUrl:       run.Links.MeshstackBaseUrl.Href,
		WorkspaceIdentifier:    &spec.WorkspaceIdentifier,
		ProjectIdentifier:      &spec.ProjectIdentifier,
		FullPlatformIdentifier: &spec.FullPlatformIdentifier,
		Behavior:               behavior,
		BuildingBlockId:        run.Spec.BuildingBlock.Uuid,
		BuildingBlockName:      spec.DisplayName,
		IsAsync:                impl.Async,
		Vars:                   vars,
		RunJsonBase64:          base64.StdEncoding.EncodeToString(runJson),
		Source:                 terraformImplToGitSource(impl),
		UseMeshBackendFallback: impl.UseMeshHttpBackendFallback,
		PreRunScript:           impl.PreRunScript,
		PlanArtifactUrl:        run.Links.PlanArtifact.Href,
		ArtifactUploadUrl:      run.Links.ArtifactUpload.Href,
		TfStateLockUrl:         run.Links.TfStateLock.Href,
	}, nil
}

func toVariables(inputs []meshapi.MeshRunnerRunInput) (map[string]*Variable, error) {
	vars := make(map[string]*Variable, len(inputs))
	for _, input := range inputs {
		var value any
		if err := json.Unmarshal(input.Value, &value); err != nil {
			return nil, fmt.Errorf("input %s: %w", input.Key, err)
		}
		vars[input.Key] = &Variable{value: value, env: input.Env, Type: DataType(input.Type)}
	}
	return vars, nil
}

func terraformImplToGitSource(impl meshapi.MeshRunnerTerraformImplementation) *GitSource {
	var gitAuth auth = &NoAuth{}
	if impl.SshPrivateKey != nil {
		gitAuth = &SshAuth{certStr: *impl.SshPrivateKey, knownHostEntry: knownHostsToInternal(impl.KnownHost)}
	}
	return &GitSource{
		url:       impl.RepositoryUrl,
		path:      impl.RepositoryPath,
		auth:      gitAuth,
		refName:   impl.RefName,
		gitFacade: &Git{},
	}
}

func knownHostsToInternal(knownHost *meshapi.MeshRunnerKnownHost) *KnownHost {
	if knownHost == nil {
		return nil
	}
	return &KnownHost{host: knownHost.Host, key: knownHost.KeyType, value: knownHost.KeyValue}
}
