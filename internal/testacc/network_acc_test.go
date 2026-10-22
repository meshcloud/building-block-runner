package testacc

import (
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	msgFinalStatus  = "sending the final status"
	msgAbortLearned = "meshStack aborted the run, cancelling it"

	blockedLongerThanKeepalive = 75 * time.Second
	// slowClaimLatency outlasts the minute that the meshStack client waits for response headers.
	slowClaimLatency = 75 * time.Second
	sshStall         = 5 * time.Second
)

func networkTfBlockCase(apiUrl string, vars map[string]any) testCase {
	return testCase{name: "tfblock", implementationType: "TERRAFORM", runner: runnerOptions{forType: "tf", apiUrl: apiUrl}, vars: vars}
}

func TestAccNetworkFinalStatusRetriedAfterResets(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)
	proxy := s.startApiProxy(t)
	g := gate{dir: s.workDir(t, "gate")}
	c := networkTfBlockCase(proxy.url(), map[string]any{"static_inputs": map[string]string{"gate_dir": g.dir}, "wait_for_completion": false})
	c.module = "network"

	d := s.deploy(t, c)

	g.awaitWaiting(t)
	removeResets := proxy.addToxic(t, "reset_peer", upstream, map[string]any{"timeout": 0})
	g.release(t)
	records := d.runner.records.awaitInOrder(t, "of the final status and a retry of it",
		func(r record) bool { return r.attr("msg") == msgFinalStatus },
		func(r record) bool { return r.attr("msg") == "retrying request" && r.attr("method") == "PATCH" },
	)
	removeResets()

	runUuid := records[0].attr("run")
	require.Equalf(t, statusSucceeded, records[0].attr("status"), "run log:\n%s", d.runLog(t, runUuid))
	assert.Equal(t, runUuid, records[1].attr("run"))
	d.awaitBuildingBlock(t, client.BuildingBlockStatusSucceeded)
	assert.Equal(t, statusSucceeded, d.readRun(t, runUuid).Status)
}

func TestAccNetworkAbortLearnedAfterBlockedUpdates(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)
	proxy := s.startApiProxy(t)

	d := s.deploy(t, testCase{
		name:               "sleep",
		implementationType: "TERRAFORM",
		runner:             runnerOptions{forType: "tf", apiUrl: proxy.url()},
		vars:               map[string]any{"sleep_seconds": 600, "approve_manual_triggers": false, "wait_for_completion": false},
	})

	runUuid := d.awaitRunInStep(t, stepExecuteTf, runTimeout)
	run := d.readRun(t, runUuid)
	require.NotNil(t, run.Links.Abort, "meshStack offers no abort link for the run")
	unblock := proxy.addToxic(t, "timeout", upstream, map[string]any{"timeout": 0})
	require.NoError(t, s.api.BuildingBlockRunAction.AbortRun(t.Context(), *run.Links.Abort))
	time.Sleep(blockedLongerThanKeepalive)
	learned := func(r record) bool { return r.attr("msg") == msgAbortLearned }
	require.False(t, slices.ContainsFunc(d.runner.records.ofRun(runUuid), learned), "the run learned of the abort while meshStack was unreachable")
	assertRecord(t, d.runner.records.ofRun(runUuid), "of a status update that failed", func(r record) bool { return r.attr("msg") == "status update failed" })
	unblock()

	d.awaitBuildingBlock(t, client.BuildingBlockStatusAborted)
	records := d.runner.records.ofRun(runUuid)
	assertRecord(t, records, "of the run that it learned of the abort", learned)
	assertRecord(t, records, "of the run that it sends ABORTED", func(r record) bool {
		return r.attr("msg") == msgFinalStatus && r.attr("status") == statusAborted
	})
	d.bootstrap.destroy(t, buildingBlockResource)
}

// Pins the known hazard that meshfed commits a claim before it answers, so a claim that times out loses its run.
func TestAccNetworkSlowClaimAnswerLosesRun(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)
	proxy := s.startApiProxy(t)
	c := networkTfBlockCase(proxy.url(), map[string]any{"wait_for_completion": false, "purge_on_delete": true})
	c.withoutBuildingBlock = true

	d := s.deploy(t, c)
	d.runner.records.await(t, "of the runner that it claims", func(r record) bool { return r.attr("msg") == "claiming runs" })
	unblockClaims := proxy.addToxic(t, "timeout", upstream, map[string]any{"timeout": 0})
	t.Cleanup(func() { d.destroyUnlessKept(t, buildingBlockResource) })
	d.bootstrap.apply(t)
	d.buildingBlockUuid = d.bootstrap.output(t, "building_block_uuid")
	require.Eventually(t, func() bool { return d.readBuildingBlock(t).Status.LatestRunUuid != nil }, time.Minute, pollInterval)
	runUuid := d.latestRunUuid(t)
	delayAnswers := proxy.addToxic(t, "latency", downstream, map[string]any{"latency": slowClaimLatency.Milliseconds()})
	unblockClaims()
	d.runner.records.await(t, "of the runner that its claim timed out", func(r record) bool {
		return r.attr("msg") == "claiming a run failed" && strings.Contains(r.attr("error"), "timeout awaiting response headers")
	})
	delayAnswers()

	started := func(r record) bool { return r.attr("msg") == "starting run" }
	assert.Never(t, func() bool { return slices.ContainsFunc(d.runner.records.ofRun(runUuid), started) }, 30*time.Second, pollInterval,
		"the runner started the run whose claim answer it missed")
	assert.Equal(t, statusInProgress, d.readRun(t, runUuid).Status)
	assert.Equal(t, client.BuildingBlockStatusInProgress, d.readBuildingBlock(t).Status.Status)
}

func TestAccNetworkSshCloneRetriesAfterStall(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)
	key := newSshClientKey(t)
	repoDir := strings.TrimPrefix(s.commitModule(t, "ssh"), "file://")
	server := startSshGitServer(t, repoDir, key.publicKey)
	proxy := startToxicProxy(t, server.listener.Addr().String())
	_, proxyPort, err := net.SplitHostPort(proxy.addr())
	require.NoError(t, err)
	knownHost := server.knownHost()
	knownHost["host"] = "[127.0.0.1]:" + proxyPort
	removeStall := []func(){
		proxy.addToxic(t, "timeout", upstream, map[string]any{"timeout": 0}),
		proxy.addToxic(t, "timeout", downstream, map[string]any{"timeout": 0}),
	}
	var stalled sync.WaitGroup
	stalled.Go(func() {
		select {
		case <-proxy.firstClient:
			time.Sleep(sshStall)
		case <-t.Context().Done():
		}
		// Removing a timeout toxic closes the connection that it stalled.
		for _, remove := range removeStall {
			remove()
		}
	})

	d := s.deploy(t, testCase{name: "ssh", implementationType: "TERRAFORM", runner: tfRunner, vars: map[string]any{
		"repository_url":  "ssh://git@" + proxy.addr() + repoDir,
		"ssh_private_key": key.privateKeyPem,
		"ssh_known_host":  knownHost,
		"purge_on_delete": false,
	}})
	stalled.Wait()

	bb := d.readBuildingBlock(t)
	runLog := d.runLog(t, d.latestRunUuid(t))
	require.Equalf(t, client.BuildingBlockStatusSucceeded, bb.Status.Status, "run log:\n%s", runLog)
	displayUrl := "ssh://" + proxy.addr() + repoDir
	assert.Contains(t, runLog, "git clone of "+displayUrl+" failed in attempt 1 of 3")
	assert.Contains(t, runLog, "Cloned "+displayUrl+".")
}

func (l *recordLog) awaitInOrder(t *testing.T, description string, matchers ...func(record) bool) []record {
	t.Helper()
	var found []record
	require.Eventuallyf(t, func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		found = found[:0]
		for _, r := range l.records {
			if len(found) < len(matchers) && matchers[len(found)](r) {
				found = append(found, r)
			}
		}
		return len(found) == len(matchers)
	}, runTimeout, time.Second, "no records %s", description)
	return found
}

type gate struct {
	dir string
}

func (g gate) awaitWaiting(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool { return exists(filepath.Join(g.dir, "waiting")) }, runTimeout, time.Second, "no run reached the gate")
}

func (g gate) release(t *testing.T) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(g.dir, "released"), nil, 0o600))
}
