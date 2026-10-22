package testacc

import (
	"context"
	"crypto/rand"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/meshcloud/meshstack-cli/client/types/enum"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/executor"
)

const (
	mockPipelineStep  = "Mock pipeline"
	mockReportTimeout = time.Minute
	maxReportFailures = 10
)

type pipelineType struct {
	testCase              string
	implementationType    string
	runImplementationType enum.Entry[client.MeshRunnerImplementationType]
	forType               string
}

var (
	githubWorkflow = pipelineType{"github", "GITHUB_WORKFLOW", client.MeshRunnerImplementationTypeGithubWorkflow, "github"}
	gitlabPipeline = pipelineType{"gitlab", "GITLAB_PIPELINE", client.MeshRunnerImplementationTypeGitlabCiCd, "gitlab"}
	azureDevOps    = pipelineType{"azuredevops", "AZURE_DEVOPS_PIPELINE", client.MeshRunnerImplementationTypeAzureDevops, "azureDevOps"}
)

// requireExecutor runs the runner binary in its run mode, which names a missing executor before it contacts meshStack.
func requireExecutor(t *testing.T, pt pipelineType) *accSuite {
	t.Helper()
	s := requireSuite(t)
	run := client.MeshRunnerRun{Spec: client.MeshRunnerRunSpec{Definition: client.MeshRunnerRunDefinition{Spec: client.MeshRunnerRunDefinitionSpec{
		Implementation: client.MeshRunnerRunImplementation{Type: pt.runImplementationType},
	}}}}
	encodedRun, err := json.Marshal(run)
	require.NoError(t, err)
	runYml := filepath.Join(t.TempDir(), "run.yml")
	require.NoError(t, os.WriteFile(runYml, encodedRun, 0o600))
	probe := exec.CommandContext(t.Context(), s.runnerBinary)
	probe.Env = []string{executor.EnvRunYml + "=" + runYml, executor.EnvRunnerUuid + "=" + uuid.New().String(), "GOCOVERDIR=" + s.coverDir}
	output, _ := probe.CombinedOutput()
	if strings.Contains(string(output), "this runner cannot run") {
		t.Skipf("the runner binary has no executor for %s runs yet; this case specifies its Go port", pt.runImplementationType)
	}
	return s
}

func (s *accSuite) deployPipeline(t *testing.T, pt pipelineType, vars map[string]any) deployment {
	t.Helper()
	return s.deploy(t, testCase{name: pt.testCase, implementationType: pt.implementationType, runner: runnerOptions{forType: pt.forType}, vars: vars})
}

type pipelineMock struct {
	server    *httptest.Server
	mux       *http.ServeMux
	meshStack client.MeshRunnerClient
}

func newPipelineMock(meshStack client.MeshRunnerClient) *pipelineMock {
	mux := http.NewServeMux()
	return &pipelineMock{server: httptest.NewServer(mux), mux: mux, meshStack: meshStack}
}

func (m *pipelineMock) mount(handler http.Handler) (baseUrl string) {
	prefix := "/" + strings.ToLower(rand.Text())
	m.mux.Handle(prefix+"/", http.StripPrefix(prefix, handler))
	return m.server.URL + prefix
}

type pipelineTrigger struct {
	ref    string
	params map[string]string
}

// pipeline records its triggers and plays the pipeline they start. Async, it reports to meshStack with the run
// token as a real pipeline does; sync, it leaves the reporting to the runner, which polls its runs.
type pipeline struct {
	meshStack client.MeshRunnerClient
	// outcome is the final status of a run, and empty for a run that stays in progress.
	outcome string
	async   bool

	mu       sync.Mutex
	triggers []pipelineTrigger
	runs     []pipelineRun
	failures chan error
}

func (m *pipelineMock) newPipeline(t *testing.T, outcome string, async bool) *pipeline {
	t.Helper()
	p := &pipeline{meshStack: m.meshStack, outcome: outcome, async: async, failures: make(chan error, maxReportFailures)}
	t.Cleanup(func() {
		for len(p.failures) > 0 {
			t.Errorf("the mock pipeline could not report to meshStack: %v", <-p.failures)
		}
	})
	return p
}

func (p *pipeline) start(ctx context.Context, trigger pipelineTrigger, run client.MeshRunnerRun, runErr error) pipelineRun {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.triggers = append(p.triggers, trigger)
	started := pipelineRun{id: int64(len(p.runs) + 1), created: time.Now().UTC()}
	p.runs = append(p.runs, started)
	if p.async {
		// The pipeline outlives the request that triggered it.
		ctx = context.WithoutCancel(ctx)
		go func() {
			if err := errors.Join(runErr, p.report(ctx, run)); err != nil {
				p.failures <- err
			}
		}()
	}
	return started
}

func (p *pipeline) report(ctx context.Context, run client.MeshRunnerRun) error {
	if run.Spec.RunToken == "" {
		return errors.New("the trigger carried no run token")
	}
	ctx, cancel := context.WithTimeout(ctx, mockReportTimeout)
	defer cancel()
	api := p.meshStack.ForRun(run)
	source := uuid.New()
	const step = "mock-pipeline"
	err := api.RegisterSource(ctx, client.MeshRunnerRunSourceRegistration{
		Source: client.MeshRunnerRunSource{Id: source},
		Steps:  []client.MeshRunnerRunStepRegistration{{Id: step, DisplayName: mockPipelineStep}},
	})
	if err != nil {
		return fmt.Errorf("register the source: %w", err)
	}
	_, err = api.PatchStatus(ctx, source, client.MeshRunnerRunStatusUpdate{
		Status: &p.outcome,
		Steps: []client.MeshRunnerRunStepStatusUpdate{{
			Id: step, DisplayName: mockPipelineStep, Status: &p.outcome, UserMessage: new("The mock pipeline finished."),
		}},
	})
	return err
}

func (p *pipeline) received() []pipelineTrigger {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.triggers)
}

func (p *pipeline) requireOneTrigger(t *testing.T) pipelineTrigger {
	t.Helper()
	triggers := p.received()
	require.Len(t, triggers, 1)
	return triggers[0]
}

func (p *pipeline) poll(id int64) (pipelineRun, bool) {
	return p.update(id, func(run *pipelineRun) { run.polls++ })
}

func (p *pipeline) peek(id int64) (pipelineRun, bool) {
	return p.update(id, func(*pipelineRun) {})
}

func (p *pipeline) cancel(id int64) (pipelineRun, bool) {
	return p.update(id, func(run *pipelineRun) { run.canceled = true })
}

func (p *pipeline) update(id int64, change func(*pipelineRun)) (pipelineRun, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if id < 1 || id > int64(len(p.runs)) {
		return pipelineRun{}, false
	}
	change(&p.runs[id-1])
	return p.runs[id-1], true
}

func (p *pipeline) requireOneRun(t *testing.T) pipelineRun {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	require.Len(t, p.runs, 1)
	return p.runs[0]
}

func (p *pipeline) newestRuns(yield func(pipelineRun) bool) {
	p.mu.Lock()
	runs := slices.Clone(p.runs)
	p.mu.Unlock()
	for _, run := range slices.Backward(runs) {
		if !yield(run) {
			return
		}
	}
}

func (p *pipeline) pollCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	polls := 0
	for _, run := range p.runs {
		polls += run.polls
	}
	return polls
}

// progress picks the status of a run by its polls from statuses, the last of which completes it. A pipeline
// without an outcome completes only when canceled.
func (p *pipeline) progress(run pipelineRun, statuses []string) (status string, completed bool) {
	last := len(statuses) - 1
	if run.canceled {
		return statuses[last], true
	}
	if p.outcome == "" {
		last--
	}
	stage := min(run.polls, last)
	return statuses[stage], stage == len(statuses)-1
}

func (p *pipeline) succeeds() bool {
	return p.outcome == statusSucceeded
}

type pipelineRun struct {
	id       int64
	created  time.Time
	polls    int
	canceled bool
}

// runAt is the run as a pipeline sees it that got only the run URL and token.
func runAt(runUrl, runToken string) (client.MeshRunnerRun, error) {
	if runUrl == "" {
		return client.MeshRunnerRun{}, errors.New("the trigger carried no run URL")
	}
	return client.MeshRunnerRun{
		Spec: client.MeshRunnerRunSpec{RunToken: runToken},
		Links: client.MeshRunnerRunLinks{
			Self:           client.MeshRunnerRunLink{Href: runUrl},
			RegisterSource: client.MeshRunnerRunLink{Href: runUrl + "/status/source"},
			UpdateSource:   client.MeshRunnerRunLink{Href: runUrl + "/status/source/{sourceId}", Templated: true},
		},
	}, nil
}

func decodeRun(encoded []byte) (run client.MeshRunnerRun, err error) {
	if err = json.Unmarshal(encoded, &run); err != nil {
		err = fmt.Errorf("decode the run: %w", err)
	}
	return
}

func runInputs(run client.MeshRunnerRun) map[string]string {
	values := map[string]string{}
	for _, input := range run.Spec.BuildingBlock.Spec.Inputs {
		if text, isString := input.Value.(string); isString {
			values[input.Key] = text
		}
	}
	return values
}

func writeJson(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.MarshalWrite(w, body)
}

type pipelineCase struct {
	deployment

	runUuid string
	runLog  string
}

func (s *accSuite) deployPipelineCase(t *testing.T, pt pipelineType, vars map[string]any) pipelineCase {
	t.Helper()
	d := s.deployPipeline(t, pt, vars)
	runUuid := d.latestRunUuid(t)
	return pipelineCase{deployment: d, runUuid: runUuid, runLog: d.runLog(t, runUuid)}
}

func (c pipelineCase) requireStatus(t *testing.T, status enum.Entry[client.BuildingBlockStatus]) {
	t.Helper()
	require.Equalf(t, status, c.readBuildingBlock(t).Status.Status, "run log:\n%s", c.runLog)
}

func (c pipelineCase) assertTriggerStep(t *testing.T, status string) {
	t.Helper()
	logs, err := c.api.BuildingBlockRun.GetLogs(t.Context(), c.runUuid)
	require.NoError(t, err)
	require.NotEmptyf(t, logs.Steps, "run log:\n%s", c.runLog)
	assert.Equalf(t, status, logs.Steps[0].Status, "run log:\n%s", c.runLog)
}

func (c pipelineCase) assertMockStep(t *testing.T, status string) {
	t.Helper()
	assert.Contains(t, c.runLog, "## "+mockPipelineStep+" "+status)
}

func (c pipelineCase) assertHandedOver(t *testing.T) {
	t.Helper()
	c.runner.records.await(t, "of the runner that the run finished", func(r record) bool {
		return r.attr("run") == c.runUuid && r.attr("msg") == "run finished"
	})
	assertNoRecord(t, c.runner.records.ofRun(c.runUuid), "of the runner that the run failed", isRunFailed)
}

func isRunFailed(r record) bool {
	return r.attr("msg") == "run failed"
}

func assertNoRecord(t *testing.T, records []record, description string, matches func(record) bool) {
	t.Helper()
	assert.Falsef(t, slices.ContainsFunc(records, matches), "a record %s", description)
}

func (s *accSuite) abortSync(t *testing.T, pt pipelineType, p *pipeline, vars map[string]any) {
	t.Helper()
	vars["wait_for_completion"] = false
	d := s.deployPipeline(t, pt, vars)
	require.Eventually(t, func() bool { return p.pollCount() > 0 }, runTimeout, pollInterval, "the runner did not poll the pipeline run")
	runUuid := d.latestRunUuid(t)
	run := d.readRun(t, runUuid)
	require.NotNil(t, run.Links.Abort)

	require.NoError(t, s.api.BuildingBlockRunAction.AbortRun(t.Context(), *run.Links.Abort))

	d.awaitBuildingBlock(t, client.BuildingBlockStatusAborted)
	d.runner.records.await(t, "of the runner that the run finished", func(r record) bool {
		return r.attr("run") == runUuid && r.attr("msg") == "run finished"
	})
	assertNoRecord(t, d.runner.records.ofRun(runUuid), "of the runner that the run failed", isRunFailed)
	assert.True(t, p.requireOneRun(t).canceled, "the runner did not cancel the pipeline run")
}
