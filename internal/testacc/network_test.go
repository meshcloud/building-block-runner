package testacc

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/v2/internal/testacc/harness"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/runnerproc"
	"github.com/meshcloud/building-block-runner/v2/internal/testacc/toxiproxy"
)

func TestAccNetwork(t *testing.T) {
	t.Parallel()
	s := requireSuite(t)

	t.Run("FinalStatusRetriedAfterResets", func(t *testing.T) {
		t.Parallel()
		proxy := s.StartApiProxy(t)
		g := gate{dir: s.WorkDir(t, "gate")}

		d := harness.Deploy(t, s, harness.Case{
			Files:              caseFiles(t),
			Bootstrap:          tfblockBootstrap(t),
			ImplementationType: "TERRAFORM",
			Runner:             runnerproc.Options{ForType: "tf", ApiUrl: proxy.Url},
			Vars:               map[string]any{"static_inputs": map[string]string{"gate_dir": g.dir}, "wait_for_completion": false},
		})

		g.awaitWaiting(t)
		removeResets := proxy.AddToxic(t, "reset_peer", toxiproxy.Upstream, map[string]any{"timeout": 0})
		g.release(t)
		records := d.Runner.Records.AwaitInOrder(t, "of the final status and a retry of it",
			func(r runnerproc.Record) bool { return r.Attr("msg") == "sending the final status" },
			func(r runnerproc.Record) bool {
				return r.Attr("msg") == "retrying request" && r.Attr("method") == "PATCH"
			},
		)
		removeResets()

		runUuid := records[0].Attr("run")
		require.Equalf(t, succeeded, records[0].Attr("status"), "run log:\n%s", d.RunLog(t, runUuid))
		assert.Equal(t, runUuid, records[1].Attr("run"))
		d.AwaitBuildingBlock(t, client.BuildingBlockStatusSucceeded)
		assert.Equal(t, succeeded, d.ReadRun(t, runUuid).Status)
	})
}

// gate holds a run of the network module until the test releases it.
type gate struct {
	dir string
}

func (g gate) awaitWaiting(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(g.dir, "waiting"))
		return err == nil
	}, runnerproc.RunTimeout, time.Second, "no run reached the gate")
}

func (g gate) release(t *testing.T) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(g.dir, "released"), nil, 0o600))
}
