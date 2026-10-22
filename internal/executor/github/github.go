package github

import (
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	gohttp "net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/client/http"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/executor"
)

const (
	triggerStepId = "gh-trigger"

	inputRun       = "buildingBlockRun"
	inputRunUrl    = "buildingBlockRunUrl"
	inputApiToken  = "MESHSTACK_API_TOKEN" //nolint:gosec // an input name, no credential
	inputRunToken  = "MESHSTACK_RUN_TOKEN" //nolint:gosec // an input name, no credential
	inputEndpoint  = "MESHSTACK_ENDPOINT"
	unexpectedHint = "Unexpected inputs provided:"
)

func init() {
	executor.Register(config.RunnerTypeGitHub, func(struct{}) (executor.Executor, error) { return Execute, nil })
}

type workflow struct {
	api        api
	impl       client.MeshRunnerGithubImplementation
	file       string
	reporter   *executor.Reporter
	dispatched time.Time
}

func Execute(ctx context.Context, run executor.Run) error {
	trigger := run.Reporter.Step(triggerStepId, "Trigger GitHub Action")
	if err := run.Reporter.Register(ctx); err != nil {
		return err
	}
	w := &workflow{reporter: run.Reporter}
	var runId int64
	err := trigger.Run(func() error {
		var err error
		runId, err = w.dispatch(ctx, run)
		if err != nil {
			trigger.Logf("%v", err)
			trigger.UserLogf("%s", triggerFailure(err))
		}
		return err
	})
	if err != nil {
		return err
	}
	if w.impl.Async {
		trigger.UserLogf("Triggered the GitHub workflow %s, which reports its status itself.", w.file)
		run.Reporter.HandOver()
		return nil
	}
	trigger.UserLogf("Triggered the GitHub workflow %s, waiting for its run to complete.", w.file)
	trigger.SetStatus(executor.StatusInProgress)
	status, err := w.await(ctx, runId)
	trigger.SetStatus(status)
	if err != nil {
		trigger.UserLogf("%v", err)
	}
	return err
}

func triggerFailure(err error) string {
	if unexpected, ok := errors.AsType[unexpectedInputsError](err); ok {
		return unexpected.help()
	}
	return "Could not trigger the GitHub Action."
}

// dispatch returns the id of the workflow run, or 0 when GitHub did not tell it.
func (w *workflow) dispatch(ctx context.Context, run executor.Run) (int64, error) {
	impl := run.Spec.Definition.Spec.Implementation.GithubWorkflow
	if impl == nil {
		return 0, errors.New("the run has no GitHub workflow implementation")
	}
	w.impl = *impl
	file, err := workflowFile(w.impl, run.Spec.Behavior)
	if err != nil {
		return 0, err
	}
	w.file = file
	inputs := workflowInputs(run, w.impl.OmitRunObjectInput)
	app, err := newApi(w.impl.GithubBaseUrl)
	if err != nil {
		return 0, err
	}
	token, err := appJwt(w.impl.AppId, w.impl.AppPem, time.Now())
	if err != nil {
		return 0, err
	}
	if w.api, err = installationApi(ctx, app, token, w.impl.Owner, w.impl.Repository); err != nil {
		return 0, err
	}

	w.dispatched = time.Now()
	answer, err := w.api.do[[]byte](ctx, http.MethodPost, w.workflowUrl("dispatches"), http.WithJsonPayload(map[string]any{
		"ref":                w.impl.Branch,
		"inputs":             inputs,
		"return_run_details": true,
	}, "application/json"))
	if err != nil {
		return 0, w.dispatchError(err)
	}
	var dispatched struct {
		WorkflowRunId int64 `json:"workflow_run_id"`
	}
	if len(answer) > 0 {
		if err := json.Unmarshal(answer, &dispatched); err != nil {
			return 0, fmt.Errorf("decode the answer to the dispatch of the GitHub workflow %s: %w", w.file, err)
		}
	}
	slog.InfoContext(ctx, "dispatched the GitHub workflow", "repository", w.impl.Owner+"/"+w.impl.Repository, "workflow", w.file, "workflowRun", dispatched.WorkflowRunId)
	return dispatched.WorkflowRunId, nil
}

func (w *workflow) workflowUrl(segments ...string) *url.URL {
	return w.api.repoUrl(w.impl.Owner, w.impl.Repository, append([]string{"actions", "workflows", w.file}, segments...)...)
}

func workflowFile(impl client.MeshRunnerGithubImplementation, behavior string) (string, error) {
	if behavior != "DESTROY" {
		return impl.ApplyWorkflow, nil
	}
	if impl.DestroyWorkflow == nil || *impl.DestroyWorkflow == "" {
		return "", errors.New("the building block definition has no destroy workflow")
	}
	return *impl.DestroyWorkflow, nil
}

func workflowInputs(run executor.Run, omitRunObjectInput bool) map[string]string {
	if !omitRunObjectInput {
		return map[string]string{inputRun: base64.StdEncoding.EncodeToString(run.Json)}
	}
	inputs := map[string]string{inputRunUrl: run.Links.Self.Href}
	values := map[string]string{}
	for _, input := range run.Spec.BuildingBlock.Spec.Inputs {
		values[input.Key] = fmt.Sprint(input.Value)
	}
	for _, key := range []string{inputApiToken, inputRunToken} {
		if value, found := values[key]; found {
			inputs[key] = value
		}
	}
	if endpoint, found := values[inputEndpoint]; found && inputs[inputApiToken] != "" {
		inputs[inputEndpoint] = endpoint
	}
	return inputs
}

type unexpectedInputsError struct {
	error

	workflow           string
	inputs             []string
	omitRunObjectInput bool
}

func (w *workflow) dispatchError(err error) error {
	answer, ok := errors.AsType[http.Error](err)
	if !ok || answer.StatusCode != gohttp.StatusUnprocessableEntity {
		return fmt.Errorf("dispatch the GitHub workflow %s: %w", w.file, err)
	}
	var body struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(answer.ResponseBody, &body) != nil {
		return fmt.Errorf("dispatch the GitHub workflow %s: %w", w.file, err)
	}
	_, listed, found := strings.Cut(body.Message, unexpectedHint)
	var inputs []string
	if !found || json.Unmarshal([]byte(strings.TrimSpace(listed)), &inputs) != nil {
		return fmt.Errorf("dispatch the GitHub workflow %s: %w", w.file, err)
	}
	slices.Sort(inputs)
	return unexpectedInputsError{error: answer, workflow: w.file, inputs: inputs, omitRunObjectInput: w.impl.OmitRunObjectInput}
}

func (e unexpectedInputsError) help() string {
	lines := make([]string, 0, len(e.inputs))
	for _, input := range e.inputs {
		lines = append(lines, e.inputHelp(input))
	}
	return strings.Join(lines, "\n")
}

func (e unexpectedInputsError) inputHelp(input string) string {
	missing := fmt.Sprintf("The GitHub workflow %s does not declare the input %s.", e.workflow, input)
	switch {
	case input == inputRunUrl && e.omitRunObjectInput:
		return missing + " The building block definition passes only the API URL of the run, so update the workflow to read the run from that URL, as https://github.com/meshcloud/actions-register-source/releases/tag/v2.0.0 describes."
	case input == inputRun && !e.omitRunObjectInput:
		return missing + " Enable \"Pass only API URL\" in the building block definition to pass the URL of the run instead."
	case input == inputApiToken:
		return missing + " It holds a short-lived token for the meshStack API; add it to workflow_dispatch as an optional string input."
	case input == inputRunToken:
		return missing + " It holds the token that updates the status of the run; add it to workflow_dispatch as an optional string input."
	}
	return missing + " Add it to workflow_dispatch."
}
