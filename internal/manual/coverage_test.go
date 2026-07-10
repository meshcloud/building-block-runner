package manual

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/meshapitest"
)

// TestRealClock_Wait_RespectsCancel proves the real clock returns immediately when ctx is
// already cancelled (rather than blocking for the full duration), and exercises defaultRand.
func TestRealClock_Wait_RespectsCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { RealClock{}.Wait(ctx, time.Hour); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RealClock.Wait did not honor context cancellation")
	}
	require.Zero(t, defaultRand()) // 0 => SUCCEEDED branch by default
}

// TestExecute_NilDepsEcho constructs the handler with only a reporter (nil Clock/Rand/Log)
// so NewHandler's defaulting branches run, and drives the echo path end to end.
func TestExecute_NilDepsEcho(t *testing.T) {
	srv := meshapitest.NewServer(t)
	run := buildRun(t, "tok", []meshapi.BuildingBlockInputSpecDTO{{Key: "k", Value: "v", Type: typeString}})
	h := NewHandler(Config{Uuid: testUuid}, HandlerDeps{Reporters: factoryFor(srv)})
	require.NoError(t, h.Execute(context.Background(), run))
	require.Len(t, srv.Patches(), 1)
}

// TestExecute_RegisterFailureReturnsError covers Execute's register-error return.
func TestExecute_RegisterFailureReturnsError(t *testing.T) {
	srv := meshapitest.NewServer(t)
	srv.SeedRegisterResponse(500)
	run := buildRun(t, "tok", nil)
	h := NewHandler(Config{Uuid: testUuid}, HandlerDeps{Reporters: factoryFor(srv), Log: testLog()})
	require.Error(t, h.Execute(context.Background(), run))
	require.Empty(t, srv.Patches(), "no update after a failed register")
}

// TestDecodeInputs_Fallbacks covers the RawJson-empty (use Details) and invalid-base64
// (warn + fall back to Details) branches.
func TestDecodeInputs_Fallbacks(t *testing.T) {
	details := &meshapi.RunDetailsDTO{Metadata: meshapi.RunMetaDTO{Uuid: testUuid}}
	details.Spec.BuildingBlock.Spec.Inputs = []meshapi.BuildingBlockInputSpecDTO{{Key: "k", Value: "v", Type: typeString}}

	for _, raw := range []string{"", "!!!not-base64!!!"} {
		srv := meshapitest.NewServer(t)
		run := dispatch.ClaimedRun{Id: dispatch.RunId(testUuid), Details: details, RawJson: raw}
		h := NewHandler(Config{Uuid: testUuid}, HandlerDeps{Reporters: factoryFor(srv), Log: testLog()})
		require.NoError(t, h.Execute(context.Background(), run))
		upd := decodePatch(t, srv.Patches()[0].Body)
		require.Equal(t, "v", upd.Steps[0].Outputs["k"].Value)
	}
}

// TestDecodeInputs_InvalidJson covers the decode-error return (valid base64, bad JSON) —
// Execute registers first, then fails decoding.
func TestDecodeInputs_InvalidJson(t *testing.T) {
	srv := meshapitest.NewServer(t)
	run := dispatch.ClaimedRun{Id: dispatch.RunId(testUuid), RawJson: base64.StdEncoding.EncodeToString([]byte("{bad"))}
	h := NewHandler(Config{Uuid: testUuid}, HandlerDeps{Reporters: factoryFor(srv), Log: testLog()})
	require.Error(t, h.Execute(context.Background(), run))
}

// TestSingleRun_UnreadableFile covers RunSingleRun's ReadFile error branch.
func TestSingleRun_UnreadableFile(t *testing.T) {
	t.Setenv(envRunJsonFilePath, t.TempDir()+"/does-not-exist.json")
	require.Equal(t, 1, RunSingleRun(context.Background(), testLog(), Config{Uuid: testUuid}, meshapi.Identity{}))
}

// TestLoadConfig_MalformedYaml covers LoadConfig's Load error return.
func TestLoadConfig_MalformedYaml(t *testing.T) {
	writeConfig(t, "\tthis: : is not: valid: yaml\n  - broken")
	_, err := LoadConfig(testLog(), "v", false)
	require.Error(t, err)
}
