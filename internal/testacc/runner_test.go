package testacc

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json/v2"
	"encoding/pem"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const runnerStopTimeout = 3 * time.Minute

type runnerKeyPair struct {
	privateKeyPem string
	publicKeyPem  string
}

func newRunnerKeyPair(t *testing.T) runnerKeyPair {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	privateDer, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	publicDer, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	return runnerKeyPair{
		privateKeyPem: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDer})),
		publicKeyPem:  string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDer})),
	}
}

type runnerOptions struct {
	forType string
	// timeout is the dispatcher's default when empty.
	timeout          string
	registerAs       string
	insecureHostKeys bool
	metricsPort      int
	apiUrl           string
	keycloakWif      map[string]any
}

func (o runnerOptions) dispatcher() map[string]any {
	dispatcher := map[string]any{"forTypes": []string{o.forType}, "pollingInterval": "1s"}
	if o.timeout != "" {
		dispatcher["timeout"] = o.timeout
	}
	if o.registerAs != "" {
		dispatcher["register"] = map[string]any{"displayName": o.registerAs}
	}
	if o.keycloakWif != nil {
		dispatcher["keycloakWif"] = o.keycloakWif
	}
	return dispatcher
}

type runnerProcess struct {
	uuid    string
	cmd     *exec.Cmd
	records *recordLog
	exited  chan struct{}
}

// startRunner stops the runner when the test ends, so that cleanups registered later run while it still claims.
func (s *accSuite) startRunner(t *testing.T, runnerUuid, workspace string, keys runnerKeyPair, options runnerOptions) *runnerProcess {
	t.Helper()
	dir := s.workDir(t, "runner")
	configFile := filepath.Join(dir, "runner-config.yml")
	// A JSON document is YAML too.
	runnerConfig, err := json.Marshal(map[string]any{
		"uuid": runnerUuid,
		"api": map[string]any{
			"url": s.endpoint,
			"auth": map[string]any{
				"workspace": workspace,
				"key":       map[string]any{"clientId": s.runnerKey.ClientId, "clientSecret": s.runnerKey.ClientSecret},
			},
		},
		"crypto":     map[string]any{"privateKeys": []string{keys.privateKeyPem}},
		"dispatcher": options.dispatcher(),
		"tf":         map[string]any{"cacheDir": s.tfCacheDir, "insecureHostKeys": options.insecureHostKeys},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configFile, runnerConfig, 0o600))

	home, tmp := filepath.Join(dir, "home"), filepath.Join(dir, "tmp")
	require.NoError(t, os.Mkdir(home, 0o700))
	require.NoError(t, os.Mkdir(tmp, 0o700))
	cmd := exec.CommandContext(context.WithoutCancel(t.Context()), s.runnerBinary)
	cmd.Dir = dir
	cmd.Env = []string{
		"RUNNER_CONFIG_FILE=" + configFile,
		"RUNNER_LOG_FORMAT=json",
		"RUNNER_LOG_LEVEL=debug",
		"GOCOVERDIR=" + s.coverDir,
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"TMPDIR=" + tmp,
		"MESHSTACK_SKIP_VERSION_CHECK=1",
	}
	if options.metricsPort != 0 {
		cmd.Env = append(cmd.Env, "RUNNER_METRICS_PORT="+strconv.Itoa(options.metricsPort))
	}
	if options.apiUrl != "" {
		cmd.Env = append(cmd.Env, "RUNNER_API_URL="+options.apiUrl)
	}
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	cmd.Stderr = cmd.Stdout
	require.NoError(t, cmd.Start())

	runner := &runnerProcess{uuid: runnerUuid, cmd: cmd, records: &recordLog{}, exited: make(chan struct{})}
	go func() {
		defer close(runner.exited)
		runner.records.read(stdout)
		_ = cmd.Wait()
	}()
	t.Cleanup(func() {
		runner.stop(t)
		if t.Failed() {
			t.Logf("log of runner %s:\n%s", runnerUuid, runner.records.raw())
		}
	})
	return runner
}

func (r *runnerProcess) stop(t *testing.T) {
	t.Helper()
	require.NoError(t, r.cmd.Process.Signal(syscall.SIGTERM))
	select {
	case <-r.exited:
		assert.Equal(t, 0, r.cmd.ProcessState.ExitCode(), "the runner exits 0 after SIGTERM once its runs are done")
	case <-time.After(runnerStopTimeout):
		assert.Fail(t, "the runner did not stop after SIGTERM", "waited %s", runnerStopTimeout)
		_ = r.cmd.Process.Kill()
	}
}

// record is a JSON slog record of the runner or of a run process it started.
type record map[string]any

func (r record) String() string {
	encoded, _ := json.Marshal(r)
	return string(encoded)
}

func (r record) attr(key string) string {
	value, _ := r[key].(string)
	return value
}

func (r record) belowInfo() bool {
	var level slog.Level
	return level.UnmarshalText([]byte(r.attr(slog.LevelKey))) == nil && level < slog.LevelInfo
}

type recordLog struct {
	mu      sync.Mutex
	records []record
	lines   []string
	// A debug record may hold a secret.
	atLeastInfo []string
}

func (l *recordLog) read(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(nil, 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		var parsed record
		parseErr := json.Unmarshal([]byte(line), &parsed)
		l.mu.Lock()
		l.lines = append(l.lines, line)
		if parseErr == nil {
			l.records = append(l.records, parsed)
		}
		if parseErr != nil || !parsed.belowInfo() {
			l.atLeastInfo = append(l.atLeastInfo, line)
		}
		l.mu.Unlock()
	}
}

func (l *recordLog) raw() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func (r *runnerProcess) assertNotLoggedAtInfo(t *testing.T, secret string) {
	t.Helper()
	r.records.mu.Lock()
	defer r.records.mu.Unlock()
	assert.NotContains(t, strings.Join(r.records.atLeastInfo, "\n"), secret)
}

func (l *recordLog) ofRun(runUuid string) []record {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.DeleteFunc(slices.Clone(l.records), func(r record) bool { return r.attr("run") != runUuid })
}

func (l *recordLog) await(t *testing.T, description string, matches func(record) bool) record {
	t.Helper()
	var found record
	require.Eventuallyf(t, func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		for _, r := range l.records {
			if matches(r) {
				found = r
				return true
			}
		}
		return false
	}, runTimeout, pollInterval, "no record %s", description)
	return found
}

// firstRunInStep returns the run of the first record that a step of a run logged, such as a tofu line.
func (l *recordLog) firstRunInStep(step string) (runUuid string, found bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, r := range l.records {
		if r.attr("step") == step && r.attr("run") != "" {
			return r.attr("run"), true
		}
	}
	return "", false
}
