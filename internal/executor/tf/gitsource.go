package tf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/meshcloud/meshstack-cli/client"

	tfconfig "github.com/meshcloud/building-block-runner/v2/internal/config/tf"
	"github.com/meshcloud/building-block-runner/v2/internal/executor"
)

const (
	cloneDir = "tf"
	sshDir   = "ssh"
)

// GitSource needs no cleanup of its clone, as the parent removes the run dir.
type GitSource struct {
	repository *client.MeshRunnerTerraformImplementation
	runDir     string
	step       *executor.Step
}

func (g GitSource) ModuleDir() string {
	dir := filepath.Join(g.runDir, cloneDir)
	if g.repository.RepositoryPath != nil {
		dir = filepath.Join(dir, *g.repository.RepositoryPath)
	}
	return dir
}

func (g GitSource) Fetch(ctx context.Context, cfg tfconfig.Config) error {
	g.step.Logf("Cloning %s%s", g.displayUrl(), g.insecureHint(cfg))
	if err := writeSshDir(filepath.Join(g.runDir, sshDir), g.repository, cfg.SkipHostKeyValidation); err != nil {
		g.step.Logf("Preparing the SSH config for %s failed: %v", g.displayUrl(), err)
		return err
	}
	if err := g.clone(ctx, cfg.InitRetries+1); err != nil {
		return err
	}
	if refName := g.repository.RefName; refName != nil {
		if err := g.checkout(ctx, *refName); err != nil {
			return err
		}
	}
	g.step.Logf("Cloned %s.", g.displayUrl())

	return g.checkModuleDir()
}

func (g GitSource) sshConfigPath() string {
	return filepath.Join(g.runDir, sshDir, sshConfigFile)
}

// gitEnv configures every git process of the run, including tofu's module fetches. An https transfer that stalls
// below 1 KB/s for a minute fails, so that a retry gets a chance.
func (g GitSource) gitEnv() map[string]string {
	return map[string]string{
		"GIT_TERMINAL_PROMPT": "0",
		"GIT_SSH_COMMAND":     "ssh -F " + shellQuote(g.sshConfigPath()),
		"GIT_CONFIG_COUNT":    "2",
		"GIT_CONFIG_KEY_0":    "http.lowSpeedLimit",
		"GIT_CONFIG_VALUE_0":  "1000",
		"GIT_CONFIG_KEY_1":    "http.lowSpeedTime",
		"GIT_CONFIG_VALUE_1":  "60",
	}
}

func (g GitSource) clone(ctx context.Context, attempts int) error {
	attempt := 0
	_, err := backoff.Retry(ctx,
		func() (struct{}, error) {
			attempt++
			if err := os.RemoveAll(g.repoDir()); err != nil {
				return struct{}{}, backoff.Permanent(err)
			}
			var stderr bytes.Buffer
			cmd := g.gitCmd(ctx, g.runDir, "clone", "--quiet", "--", g.repository.RepositoryUrl, cloneDir)
			cmd.Stderr = io.MultiWriter(g.step, &stderr)
			err := cmd.Run()
			if err == nil {
				return struct{}{}, nil
			}
			g.step.Logf("git clone of %s failed in attempt %d of %d: %v", g.displayUrl(), attempt, attempts, err)
			if !isTransientGitFailure(stderr.String()) {
				return struct{}{}, backoff.Permanent(err)
			}
			return struct{}{}, err
		},
		backoff.WithBackOff(&backoff.ExponentialBackOff{InitialInterval: 2 * time.Second, Multiplier: 2, MaxInterval: 8 * time.Second}),
		backoff.WithMaxTries(uint(attempts)),
		backoff.WithMaxElapsedTime(0),
	)
	return err
}

var (
	permanentGitFailures = []string{
		"authentication failed", "could not read username", "invalid username or password", "permission denied",
		"host key verification failed", "repository not found", "does not appear to be a git repository",
		"returned error: 401", "returned error: 403", "returned error: 404",
	}
	transientGitFailures = []string{
		"could not resolve host", "temporary failure in name resolution", "timed out", "connection reset",
		"connection refused", "connection closed", "broken pipe", "early eof", "the remote end hung up unexpectedly",
		"rpc failed", "operation too slow", "transfer closed", "unexpected disconnect", "returned error: 5",
		"kex_exchange_identification", "ssl_read", "gnutls",
	}
)

func isTransientGitFailure(stderr string) bool {
	stderr = strings.ToLower(stderr)
	contains := func(failure string) bool { return strings.Contains(stderr, failure) }
	return !slices.ContainsFunc(permanentGitFailures, contains) && slices.ContainsFunc(transientGitFailures, contains)
}

func (g GitSource) checkModuleDir() error {
	path := g.repository.RepositoryPath
	if path == nil {
		return nil
	}
	if _, err := os.Stat(g.ModuleDir()); err != nil {
		g.step.Logf("The path '%s' does not exist in %s, check the repository settings of the building block definition.", *path, g.displayUrl())
		return errors.New("specified path does not exist")
	}
	return nil
}

// displayUrl drops the userinfo of an https url, which may hold a token.
func (g GitSource) displayUrl() string {
	repositoryUrl := g.repository.RepositoryUrl
	parsed, err := url.Parse(repositoryUrl)
	if err != nil || parsed.User == nil {
		return repositoryUrl
	}
	parsed.User = nil
	return parsed.String()
}

func (g GitSource) insecureHint(cfg tfconfig.Config) string {
	if g.repository.SshPrivateKey != nil && cfg.SkipHostKeyValidation {
		return " without host key validation"
	}
	return ""
}

func (g GitSource) checkout(ctx context.Context, ref string) error {
	g.step.Logf("Checking out ref %s of %s", ref, g.displayUrl())
	hash, err := g.resolveRef(ctx, ref)
	if err != nil {
		g.step.Logf("Resolving ref %s of %s failed: %v", ref, g.displayUrl(), err)
		return err
	}
	return g.git(ctx, g.repoDir(), "-c", "advice.detachedHead=false", "checkout", "--quiet", "--detach", hash, "--")
}

func (g GitSource) resolveRef(ctx context.Context, ref string) (string, error) {
	for _, candidate := range []string{"refs/remotes/origin/" + ref, "refs/tags/" + ref, ref} {
		cmd := g.gitCmd(ctx, g.repoDir(), "rev-parse", "--verify", "--quiet", "--end-of-options", candidate+"^{commit}")
		cmd.Stdout = nil
		out, err := cmd.Output()
		if err == nil {
			return strings.TrimSpace(string(out)), nil
		}
		if _, notFound := errors.AsType[*exec.ExitError](err); !notFound {
			return "", err
		}
	}
	return "", fmt.Errorf("ref %s is no branch, tag or commit", ref)
}

func (g GitSource) repoDir() string {
	return filepath.Join(g.runDir, cloneDir)
}

func (g GitSource) git(ctx context.Context, dir string, args ...string) error {
	return g.gitCmd(ctx, dir, args...).Run()
}

func (g GitSource) gitCmd(ctx context.Context, dir string, args ...string) *exec.Cmd {
	env := cleanSystemEnv()
	maps.Copy(env, g.gitEnv())
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // git takes the arguments that this source builds, each as its own argument.
	cmd.Dir = dir
	cmd.Env = environ(env)
	cmd.Stdout = g.step
	cmd.Stderr = g.step
	return cmd
}

func environ(env map[string]string) []string {
	list := make([]string, 0, len(env))
	for k, v := range env {
		list = append(list, k+"="+v)
	}
	return list
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
