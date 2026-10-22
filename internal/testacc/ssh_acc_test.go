package testacc

import (
	"strings"
	"testing"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const insecureHint = " without host key validation"

type sshCase struct {
	knownHost        bool
	insecureHostKeys bool
}

func (s *accSuite) deploySsh(t *testing.T, c sshCase) (deployment, *sshGitServer) {
	t.Helper()
	key := newSshClientKey(t)
	server := startSshGitServer(t, strings.TrimPrefix(s.commitModule(t, "ssh"), "file://"), key.publicKey)
	vars := map[string]any{
		"repository_url":  server.url(),
		"ssh_private_key": key.privateKeyPem,
		"ssh_known_host":  nil,
		// The destroy run clones too, so purging keeps the cleanup independent of SSH.
		"purge_on_delete": true,
	}
	if c.knownHost {
		vars["ssh_known_host"] = server.knownHost()
	}
	runner := runnerOptions{forType: "tf", insecureHostKeys: c.insecureHostKeys}
	return s.deploy(t, testCase{name: "ssh", implementationType: "TERRAFORM", runner: runner, vars: vars}), server
}

func TestAccSshKnownHost(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)

	d, server := s.deploySsh(t, sshCase{knownHost: true})

	bb := d.readBuildingBlock(t)
	runLog := d.runLog(t, d.latestRunUuid(t))
	require.Equalf(t, client.BuildingBlockStatusSucceeded, bb.Status.Status, "run log:\n%s", runLog)
	assert.Equal(t, "hello", outputValues(bb)["text"])
	assert.Contains(t, runLog, "Cloned "+server.displayUrl()+".")
	assert.NotContains(t, runLog, insecureHint)
}

func TestAccSshInsecureHostKeys(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)

	d, server := s.deploySsh(t, sshCase{insecureHostKeys: true})

	bb := d.readBuildingBlock(t)
	runLog := d.runLog(t, d.latestRunUuid(t))
	require.Equalf(t, client.BuildingBlockStatusSucceeded, bb.Status.Status, "run log:\n%s", runLog)
	assert.Contains(t, runLog, "Cloning "+server.displayUrl()+insecureHint)
	assert.Contains(t, runLog, "Cloned "+server.displayUrl()+".")
}

func TestAccSshUnknownHost(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)

	d, _ := s.deploySsh(t, sshCase{})

	bb := d.readBuildingBlock(t)
	runLog := d.runLog(t, d.latestRunUuid(t))
	require.Equalf(t, client.BuildingBlockStatusFailed, bb.Status.Status, "run log:\n%s", runLog)
	assert.Contains(t, runLog, "Host key verification failed")
	assert.NotContains(t, runLog, insecureHint)
}
