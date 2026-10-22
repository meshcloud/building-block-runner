package gitlab

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"mime/multipart"
	gohttp "net/http"
	"net/url"
	"slices"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/client/http"
	"github.com/meshcloud/meshstack-cli/client/types/xurl"

	"github.com/meshcloud/building-block-runner/internal/executor"
)

const identityVerificationRequiredReason = "Identity verification is required in order to run CI jobs"

type formField struct {
	name, value string
}

func triggerPipeline(ctx context.Context, step *executor.Step, run executor.Run, implementation client.MeshRunnerGitlabImplementation) error {
	endpoint, err := triggerUrl(implementation)
	if err != nil {
		return err
	}
	body, contentType, err := triggerForm(ctx, run, implementation)
	if err != nil {
		return err
	}
	responseBody, err := executor.NewPipelineClient().DoRequest[[]byte](ctx, http.MethodPost, endpoint,
		http.WithBody(contentType, body.Bytes()))
	if httpErr, ok := errors.AsType[http.Error](err); ok {
		step.Logf("GitLab answered %d: %s", httpErr.StatusCode, httpErr.ResponseBody)
		return triggerError(implementation.ProjectId, httpErr.StatusCode, httpErr.ResponseBody)
	}
	if err != nil {
		return err
	}
	var pipeline struct {
		Id     int64  `json:"id"`
		WebUrl string `json:"web_url"`
	}
	if err := json.Unmarshal(responseBody, &pipeline); err != nil {
		slog.WarnContext(ctx, "GitLab triggered the pipeline with an unexpected answer", "project", implementation.ProjectId, "error", err)
	}
	step.UserLogf("Triggered the configured GitLab pipeline")
	step.Logf("Triggered pipeline %d in project %s: %s", pipeline.Id, implementation.ProjectId, pipeline.WebUrl)
	slog.InfoContext(ctx, "triggered the GitLab pipeline", "project", implementation.ProjectId, "pipeline", pipeline.Id)
	return nil
}

func triggerUrl(implementation client.MeshRunnerGitlabImplementation) (*url.URL, error) {
	var base xurl.URL
	if err := base.UnmarshalText([]byte(implementation.GitlabBaseUrl)); err != nil {
		return nil, fmt.Errorf("GitLab base URL %q: %w", implementation.GitlabBaseUrl, err)
	}
	return base.JoinPath("api", "v4", "projects", url.PathEscape(implementation.ProjectId), "trigger", "pipeline"), nil
}

func triggerForm(ctx context.Context, run executor.Run, implementation client.MeshRunnerGitlabImplementation) (*bytes.Buffer, string, error) {
	fields := []formField{
		{"token", implementation.PipelineTriggerToken},
		{"ref", implementation.RefName},
		{"variables[MESHSTACK_BEHAVIOR]", run.Spec.Behavior},
		{"variables[MESHSTACK_RUN]", string(run.Json)},
	}
	for _, input := range run.Spec.BuildingBlock.Spec.Inputs {
		value, err := formValue(input.Value)
		if err != nil {
			return nil, "", fmt.Errorf("encode input %s: %w", input.Key, err)
		}
		name := "inputs[" + input.Key + "]"
		if input.Env {
			name = "variables[" + input.Key + "]"
		}
		fields = append(fields, formField{name, value})
	}
	for _, link := range []formField{
		{"MESHSTACK_SELF_URL", run.Links.Self.Href},
		{"MESHSTACK_REGISTER_SOURCE_URL", run.Links.RegisterSource.Href},
		{"MESHSTACK_UPDATE_SOURCE_URL", run.Links.UpdateSource.Href},
		{"MESHSTACK_BASE_URL", run.Links.MeshstackBaseUrl.Href},
	} {
		if link.value == "" {
			slog.WarnContext(ctx, "the run lacks the link for a GitLab variable", "variable", link.name)
			continue
		}
		fields = append(fields, formField{"variables[" + link.name + "]", link.value})
	}

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for _, field := range fields {
		if err := form.WriteField(field.name, field.value); err != nil {
			return nil, "", err
		}
	}
	if err := form.Close(); err != nil {
		return nil, "", err
	}
	return &body, form.FormDataContentType(), nil
}

func formValue(value any) (string, error) {
	if text, isString := value.(string); isString {
		return text, nil
	}
	encoded, err := json.Marshal(value)
	return string(encoded), err
}

func triggerError(projectId string, status int, body []byte) error {
	switch {
	case status == gohttp.StatusNotFound:
		return fmt.Errorf("GitLab answered 404, so the project id %s or the pipeline trigger token is wrong", projectId)
	case identityVerificationRequired(body):
		return errors.New("the GitLab account of the pipeline trigger token is not verified and cannot run a pipeline; verify it in GitLab first")
	}
	return fmt.Errorf("GitLab answered %d", status)
}

func identityVerificationRequired(body []byte) bool {
	var gitlabError struct {
		Message struct {
			Base []string `json:"base"`
		} `json:"message"`
	}
	return json.Unmarshal(body, &gitlabError) == nil && slices.Contains(gitlabError.Message.Base, identityVerificationRequiredReason)
}
