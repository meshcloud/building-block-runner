package testacc

import (
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/v2/containers"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/harness"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/runnerproc"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/sshgit"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/toxiproxy"
)

const (
	insecureHint = " without host key validation"
	sshStall     = 5 * time.Second

	childModuleRoot = `module "child" {
  source = "git::%s?ref=main"
}

output "text" {
  value = module.child.text
}
`
)

type sshCase struct {
	knownHost         bool
	knownHostsFromEnv bool
	insecureHostKeys  bool
	childModule       bool
}

func TestAccSsh(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)

	t.Run("KnownHost", func(t *testing.T) {
		t.Parallel()

		d, displayUrl := deploySsh(t, s, sshCase{knownHost: true})

		runLog := d.RequireStatus(t, client.BuildingBlockStatusSucceeded)
		assert.Equal(t, "hello", d.Output[string](t, "text"))
		assert.Contains(t, runLog, "Cloned "+displayUrl+".")
		assert.NotContains(t, runLog, insecureHint)
	})

	t.Run("ChildModule", func(t *testing.T) {
		t.Parallel()

		d, _ := deploySsh(t, s, sshCase{knownHost: true, childModule: true})

		runLog := d.RequireStatus(t, client.BuildingBlockStatusSucceeded)
		assert.Equal(t, "hello from the child module", d.Output[string](t, "text"), "tofu fetched the child module over SSH with the definition's key")
		assert.NotContains(t, runLog, "PRIVATE KEY")
		d.Runner.Records.AssertNotLoggedAtInfo(t, "PRIVATE KEY")
	})

	t.Run("KnownHostsFromEnv", func(t *testing.T) {
		t.Parallel()

		d, displayUrl := deploySsh(t, s, sshCase{knownHostsFromEnv: true})

		runLog := d.RequireStatus(t, client.BuildingBlockStatusSucceeded)
		assert.Contains(t, runLog, "Cloned "+displayUrl+".")
		assert.NotContains(t, runLog, insecureHint)
	})

	t.Run("InsecureHostKeys", func(t *testing.T) {
		t.Parallel()

		d, displayUrl := deploySsh(t, s, sshCase{insecureHostKeys: true})

		runLog := d.RequireStatus(t, client.BuildingBlockStatusSucceeded)
		assert.Contains(t, runLog, "Cloning "+displayUrl+insecureHint)
		assert.Contains(t, runLog, "Cloned "+displayUrl+".")
	})

	t.Run("UnknownHost", func(t *testing.T) {
		t.Parallel()

		d, _ := deploySsh(t, s, sshCase{})

		runLog := d.RequireStatus(t, client.BuildingBlockStatusFailed)
		assert.Contains(t, runLog, "Host key verification failed")
		assert.NotContains(t, runLog, insecureHint)
	})

	t.Run("CloneRetriesAfterStall", func(t *testing.T) {
		t.Parallel()
		key := sshgit.NewClientKey(t)
		repoDir := s.CommitRepository(t, caseModule(t))
		server := sshgit.Start(t, key.PublicKey, repoDir)
		proxy := toxiproxy.Start(t, server.Addr)
		_, proxyPort, err := net.SplitHostPort(proxy.Addr)
		require.NoError(t, err)
		knownHost := server.KnownHost(t)
		knownHost["host"] = "[127.0.0.1]:" + proxyPort
		removeStall := []func(){
			proxy.AddToxic(t, "timeout", toxiproxy.Upstream, map[string]any{"timeout": 0}),
			proxy.AddToxic(t, "timeout", toxiproxy.Downstream, map[string]any{"timeout": 0}),
		}
		var stalled sync.WaitGroup
		stalled.Go(func() {
			select {
			case <-proxy.FirstClient:
				time.Sleep(sshStall)
			case <-t.Context().Done():
			}
			// Removing a timeout toxic closes the connection that it stalled.
			for _, remove := range removeStall {
				remove()
			}
		})

		d := harness.Deploy(t, s, harness.Case{Files: caseFiles(t), ImplementationType: "TERRAFORM", Runner: runnerproc.Options{ForType: "tf"}, Vars: map[string]any{
			"repository_url":  "ssh://git@" + proxy.Addr + repoDir,
			"ssh_private_key": key.PrivateKeyPem,
			"ssh_known_host":  knownHost,
			"purge_on_delete": false,
		}})
		stalled.Wait()

		runLog := d.RequireStatus(t, client.BuildingBlockStatusSucceeded)
		displayUrl := "ssh://" + proxy.Addr + repoDir
		assert.Contains(t, runLog, "git clone of "+displayUrl+" failed in attempt 1 of 4")
		assert.Contains(t, runLog, "Cloned "+displayUrl+".")
	})
}

func deploySsh(t *testing.T, s harness.Suite, c sshCase) (harness.Deployment, string) {
	t.Helper()
	key := sshgit.NewClientKey(t)
	server := sshgit.Start(t, key.PublicKey)
	root := caseModule(t)
	if c.childModule {
		child, err := fs.Sub(caseFiles(t), "module")
		require.NoError(t, err)
		childDir := s.CommitRepository(t, child)
		server.AddRepository(t, childDir)
		root = fstest.MapFS{"main.tf": {Data: fmt.Appendf(nil, childModuleRoot, server.Url(t, childDir))}}
	}
	rootDir := s.CommitRepository(t, root)
	server.AddRepository(t, rootDir)
	vars := map[string]any{
		"repository_url":  server.Url(t, rootDir),
		"ssh_private_key": key.PrivateKeyPem,
		"ssh_known_host":  nil,
		// The destroy run clones too, so purging keeps the cleanup independent of SSH.
		"purge_on_delete": true,
	}
	if c.knownHost {
		vars["ssh_known_host"] = server.KnownHost(t)
	}
	runner := runnerproc.Options{ForType: "tf", InsecureHostKeys: c.insecureHostKeys}
	if c.knownHostsFromEnv {
		runner.SshKnownHosts = writeKnownHosts(t, server)
	}
	d := harness.Deploy(t, s, harness.Case{Files: caseFiles(t), ImplementationType: "TERRAFORM", Runner: runner, Vars: vars})
	return d, server.DisplayUrl(t, rootDir)
}

func writeKnownHosts(t *testing.T, server *sshgit.Server) string {
	t.Helper()
	knownHost := server.KnownHost(t)
	file := filepath.Join(t.TempDir(), "known_hosts")
	content := fmt.Sprintf("%s%s %s %s\n", containers.KnownHosts, knownHost["host"], knownHost["key_type"], knownHost["key_value"])
	require.NoError(t, os.WriteFile(file, []byte(content), 0o600))
	return file
}
