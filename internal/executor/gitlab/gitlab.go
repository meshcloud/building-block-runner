package gitlab

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/client/http"
	"github.com/meshcloud/meshstack-cli/client/types/xurl"

	"github.com/meshcloud/building-block-runner/v2/internal/config"
	"github.com/meshcloud/building-block-runner/v2/internal/executor"
)

func init() {
	executor.Register(config.RunnerTypeGitLab, func(struct{}) (executor.Executor, error) { return Execute, nil })
}

func Execute(ctx context.Context, run executor.Run) error {
	step := run.Reporter.Step("gl-trigger", "Trigger GitLab CI/CD")
	if err := run.Reporter.Register(ctx); err != nil {
		return err
	}
	err := step.Run(func() error {
		implementation := run.Spec.Definition.Spec.Implementation.GitlabCiCd
		if implementation == nil {
			return errors.New("the run has no GitLab implementation")
		}
		if err := triggerPipeline(ctx, step, run, *implementation); err != nil {
			return fmt.Errorf("could not trigger the GitLab pipeline: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	run.Reporter.HandOver()
	return nil
}

const identityVerificationRequiredReason = "Identity verification is required in order to run CI jobs"

func triggerPipeline(ctx context.Context, step *executor.Step, run executor.Run, implementation client.MeshBuildingBlockRunGitlabImplementation) error {
	endpoint, err := triggerUrl(implementation)
	if err != nil {
		return err
	}
	form, err := triggerForm(ctx, run, implementation)
	if err != nil {
		return err
	}
	responseBody, err := http.ReadAllBody(executor.NewHttpClient().DoRequest(ctx, http.MethodPost, endpoint, http.WithFormRequest(form), http.WithoutRedirects()))
	if httpErr, ok := errors.AsType[http.Error](err); ok {
		step.Logf("GitLab answered %d: %s", httpErr.StatusCode, httpErr.ResponseBody)
		return triggerError(implementation.ProjectId, httpErr)
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

func triggerUrl(implementation client.MeshBuildingBlockRunGitlabImplementation) (*url.URL, error) {
	var base xurl.URL
	if err := base.UnmarshalText([]byte(implementation.GitlabBaseUrl)); err != nil {
		return nil, fmt.Errorf("GitLab base URL %q: %w", implementation.GitlabBaseUrl, err)
	}
	return base.JoinPath("api", "v4", "projects", url.PathEscape(implementation.ProjectId), "trigger", "pipeline"), nil
}

func triggerForm(ctx context.Context, run executor.Run, implementation client.MeshBuildingBlockRunGitlabImplementation) (map[string]string, error) {
	form := map[string]string{
		"token":                         implementation.PipelineTriggerToken,
		"ref":                           implementation.RefName,
		"variables[MESHSTACK_BEHAVIOR]": run.Spec.Behavior,
		"variables[MESHSTACK_RUN]":      string(run.Json),
	}
	for _, input := range run.Spec.BuildingBlock.Spec.Inputs {
		value, err := formValue(input.Value)
		if err != nil {
			return nil, fmt.Errorf("encode input %s: %w", input.Key, err)
		}
		name := "inputs[" + input.Key + "]"
		if input.Env {
			name = "variables[" + input.Key + "]"
		}
		form[name] = value
	}
	for name, href := range map[string]string{
		"MESHSTACK_SELF_URL":            run.Links["self"].Href,
		"MESHSTACK_REGISTER_SOURCE_URL": run.Links["registerSource"].Href,
		"MESHSTACK_UPDATE_SOURCE_URL":   run.Links["updateSource"].Href,
		"MESHSTACK_BASE_URL":            run.Links["meshstackBaseUrl"].Href,
	} {
		if href == "" {
			slog.WarnContext(ctx, "the run lacks the link for a GitLab variable", "variable", name)
			continue
		}
		form["variables["+name+"]"] = href
	}
	return form, nil
}

// formValue renders JSON text, which GitLab parses into the type of a number, boolean or array input.
func formValue(value any) (string, error) {
	if text, isString := value.(string); isString {
		return text, nil
	}
	encoded, err := json.Marshal(value)
	return string(encoded), err
}

func triggerError(projectId string, httpErr http.Error) error {
	switch {
	case httpErr.IsNotFound():
		return fmt.Errorf("GitLab answered 404, so the project id %s or the pipeline trigger token is wrong", projectId)
	case identityVerificationRequired(httpErr):
		return errors.New("the GitLab account of the pipeline trigger token is not verified and cannot run a pipeline; verify it in GitLab first")
	}
	return fmt.Errorf("GitLab answered %d", httpErr.StatusCode)
}

func identityVerificationRequired(httpErr http.Error) bool {
	gitlabError, err := httpErr.BodyAsJson[struct {
		Message struct {
			Base []string `json:"base"`
		} `json:"message"`
	}]()
	return err == nil && slices.Contains(gitlabError.Message.Base, identityVerificationRequiredReason)
}
