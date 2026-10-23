package testacc

import (
	"crypto/rand"
	"io"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	statusSucceeded = "SUCCEEDED"
	statusFailed    = "FAILED"
	statusAborted   = "ABORTED"

	stepExecuteTf = "execute_tf"
	runTimeout    = 10 * time.Minute
)

var tfRunner = runnerOptions{forType: "tf"}

func TestAccRegistration(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)
	const displayName = "building-block-runner TestAcc, registered"
	metricsPort := freePort(t)

	d := s.deploy(t, testCase{
		name:                 "registration",
		implementationType:   "TERRAFORM",
		runner:               runnerOptions{forType: "tf", registerAs: displayName, metricsPort: metricsPort},
		withoutBuildingBlock: true,
	})

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		runner, err := s.api.BuildingBlockRunner.Read(t.Context(), d.runner.uuid)
		require.NoError(c, err)
		assert.Equal(c, displayName, runner.Spec.DisplayName)
		assert.Equal(c, "TERRAFORM", runner.Spec.ImplementationType)
		require.NotNil(c, runner.Status)
		assert.NotNil(c, runner.Status.LastSeen, "meshStack shows a runner that claims as online through lastSeen")
	}, time.Minute, pollInterval)
	assert.Contains(t, scrapeMetrics(t, metricsPort), "building_block_runner_")
}

func inputsCase(secret, expectedSecret string) testCase {
	return testCase{
		name:               "inputs",
		implementationType: "TERRAFORM",
		runner:             tfRunner,
		vars:               map[string]any{"secret": secret, "expected_secret": expectedSecret},
	}
}

func TestAccTfInputs(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)
	secret := rand.Text()

	d := s.deploy(t, inputsCase(secret, secret))

	bb := d.readBuildingBlock(t)
	runUuid := d.latestRunUuid(t)
	runLog := d.runLog(t, runUuid)
	require.Equalf(t, client.BuildingBlockStatusSucceeded, bb.Status.Status, "run log:\n%s", runLog)
	assert.Equal(t, "hello", outputValues(bb)["text"])
	assert.Contains(t, runLog, "The pre-run script saw every environment input.")
	records := d.runner.records.ofRun(runUuid)
	assertRecord(t, records, "of the runner that the run finished", func(r record) bool { return r.attr("msg") == "run finished" })
	assertRecord(t, records, "of the run that it sends SUCCEEDED", func(r record) bool {
		return r.attr("msg") == "sending the final status" && r.attr("status") == statusSucceeded
	})
	assertRecord(t, records, "of tofu apply", func(r record) bool { return r.attr("step") == stepExecuteTf })
	d.runner.assertNotLoggedAtInfo(t, secret)
}

func TestAccTfSecretMismatch(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)
	wrongSecret := rand.Text()
	const failedPostcondition = "sensitive input secret does not hold the expected secret."

	d := s.deploy(t, inputsCase(wrongSecret, rand.Text()))

	bb := d.readBuildingBlock(t)
	runUuid := d.latestRunUuid(t)
	runLog := d.runLog(t, runUuid)
	require.Equalf(t, client.BuildingBlockStatusFailed, bb.Status.Status, "run log:\n%s", runLog)
	assert.Contains(t, runLog, failedPostcondition)
	assert.NotContains(t, runLog, wrongSecret)
	assertRecord(t, d.runner.records.ofRun(runUuid), "of tofu naming the postcondition", func(r record) bool {
		return r.attr("step") == stepExecuteTf && strings.Contains(r.attr("msg"), failedPostcondition)
	})
	d.runner.assertNotLoggedAtInfo(t, wrongSecret)
}

func sleepCase(runner runnerOptions, vars map[string]any) testCase {
	defaults := map[string]any{"sleep_seconds": 0, "approve_manual_triggers": false, "wait_for_completion": true}
	maps.Copy(defaults, vars)
	return testCase{name: "sleep", implementationType: "TERRAFORM", runner: runner, vars: defaults}
}

func TestAccTimeout(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)
	const timeout = "30s"

	d := s.deploy(t, sleepCase(runnerOptions{forType: "tf", timeout: timeout}, map[string]any{"sleep_seconds": 600}))

	bb := d.readBuildingBlock(t)
	runLog := d.runLog(t, d.latestRunUuid(t))
	require.Equalf(t, client.BuildingBlockStatusFailed, bb.Status.Status, "run log:\n%s", runLog)
	assert.Contains(t, runLog, "the run exceeded the timeout of "+timeout)
}

func TestAccAbort(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)

	d := s.deploy(t, sleepCase(tfRunner, map[string]any{"sleep_seconds": 600, "wait_for_completion": false}))

	runUuid := d.awaitRunInStep(t, stepExecuteTf, runTimeout)
	run := d.readRun(t, runUuid)
	require.NotNil(t, run.Links.Abort)
	require.NoError(t, s.api.BuildingBlockRunAction.AbortRun(t.Context(), *run.Links.Abort))

	d.awaitBuildingBlock(t, client.BuildingBlockStatusAborted)
	assertRecord(t, d.runner.records.ofRun(runUuid), "of the run that it sends ABORTED", func(r record) bool {
		return r.attr("msg") == "sending the final status" && r.attr("status") == statusAborted
	})
}

func TestAccPlanApproval(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)

	d := s.deploy(t, sleepCase(tfRunner, map[string]any{"approve_manual_triggers": true}))
	require.Equal(t, client.BuildingBlockStatusSucceeded, d.readBuildingBlock(t).Status.Status)

	applyUuid, approve := d.triggerForApproval(t)
	approve()

	d.awaitRun(t, applyUuid, runTimeout, statusSucceeded, statusFailed)
	runLog := d.runLog(t, applyUuid)
	require.Equalf(t, statusSucceeded, d.readRun(t, applyUuid).Status, "run log:\n%s", runLog)
	assert.Contains(t, runLog, "Using the provided plan artifact from the dry-run")
}

func (d deployment) triggerForApproval(t *testing.T) (applyUuid string, approve func()) {
	t.Helper()
	_, err := d.api.BuildingBlockV2.TriggerRun(t.Context(), d.buildingBlockUuid)
	require.NoError(t, err)
	d.awaitBuildingBlock(t, client.BuildingBlockStatusWaitingForApproval)
	applyUuid = d.latestRunUuid(t)
	apply := d.readRun(t, applyUuid)
	require.NotNil(t, apply.Links.Approve)
	require.NotNil(t, apply.Links.Predecessor)
	detect, err := d.api.BuildingBlockRunAction.ReadRun(t.Context(), *apply.Links.Predecessor)
	require.NoError(t, err)
	assert.Equal(t, "DETECT", detect.Spec.Behavior)
	assert.Equal(t, statusSucceeded, detect.Status)
	return applyUuid, func() {
		require.NoError(t, d.api.BuildingBlockRunAction.ApproveRun(t.Context(), *apply.Links.Approve, detect.Metadata.Uuid))
	}
}

func TestAccManual(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)

	d := s.deploy(t, testCase{name: "manual", implementationType: "MANUAL", runner: runnerOptions{forType: "manual"}})

	bb := d.readBuildingBlock(t)
	require.Equalf(t, client.BuildingBlockStatusSucceeded, bb.Status.Status, "run log:\n%s", d.runLog(t, d.latestRunUuid(t)))
	assert.Equal(t, map[string]any{"text": "hello", "num": float64(42), "flag": true}, outputValues(bb))
}

func TestAccHubNoop(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)

	d := s.deploy(t, testCase{name: "noop", implementationType: "TERRAFORM", runner: tfRunner})

	bb := d.readBuildingBlock(t)
	require.Equalf(t, client.BuildingBlockStatusSucceeded, bb.Status.Status, "run log:\n%s", d.runLog(t, d.latestRunUuid(t)))
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := netip.MustParseAddrPort(listener.Addr().String()).Port()
	require.NoError(t, listener.Close())
	return int(port)
}

func scrapeMetrics(t *testing.T, port int) string {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+"/metrics", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body)
}
