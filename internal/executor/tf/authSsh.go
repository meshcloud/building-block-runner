package tf

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/meshcloud/meshstack-cli/client"
)

const (
	sshPrivateKeyFile = "private_key"
	sshKnownHostsFile = "known_hosts"
	sshConfigFile     = "ssh_config"
)

//go:embed ssh_config.tmpl
var sshConfigTmpl string

var sshConfigTemplate = template.Must(template.New(sshConfigFile).Funcs(template.FuncMap{"quote": sshConfigQuote}).Parse(sshConfigTmpl))

type sshConfig struct {
	IdentityFile    string
	KnownHostsFiles []string
	Insecure        bool
}

// writeSshDir writes an ssh_config for every run, so that its timeouts also apply to a repository without a key.
func writeSshDir(dir string, repository *client.MeshRunnerTerraformImplementation, insecureHostKeys bool) error {
	if err := os.Mkdir(dir, 0o700); err != nil {
		return err
	}
	var cfg sshConfig
	if repository.SshPrivateKey != nil {
		if err := writeSshKeys(dir, repository, &cfg); err != nil {
			return err
		}
		cfg.Insecure = insecureHostKeys
	}
	cfg.KnownHostsFiles = append(cfg.KnownHostsFiles, systemKnownHostsFiles()...)
	return writeSshConfig(filepath.Join(dir, sshConfigFile), cfg)
}

func writeSshKeys(dir string, repository *client.MeshRunnerTerraformImplementation, cfg *sshConfig) error {
	cfg.IdentityFile = filepath.Join(dir, sshPrivateKeyFile)
	if err := os.WriteFile(cfg.IdentityFile, []byte(withTrailingNewline(*repository.SshPrivateKey)), 0o600); err != nil {
		return err
	}
	if knownHost := repository.KnownHost; knownHost != nil {
		knownHosts := filepath.Join(dir, sshKnownHostsFile)
		line := fmt.Sprintf("%s %s %s\n", knownHost.Host, knownHost.KeyType, knownHost.KeyValue)
		if err := os.WriteFile(knownHosts, []byte(line), 0o600); err != nil {
			return err
		}
		cfg.KnownHostsFiles = append(cfg.KnownHostsFiles, knownHosts)
	}
	return nil
}

func writeSshConfig(path string, cfg sshConfig) error {
	var b bytes.Buffer
	if err := sshConfigTemplate.Execute(&b, cfg); err != nil {
		return err
	}
	return os.WriteFile(path, b.Bytes(), 0o600)
}

func systemKnownHostsFiles() []string {
	var files []string
	if env := os.Getenv("SSH_KNOWN_HOSTS"); env != "" {
		files = append(files, env)
	}
	if home, err := os.UserHomeDir(); err == nil {
		userKnownHosts := filepath.Join(home, ".ssh", "known_hosts")
		if _, err := os.Stat(userKnownHosts); err == nil {
			files = append(files, userKnownHosts)
		}
	}
	return files
}

func sshConfigQuote(arg string) (string, error) {
	if strings.ContainsAny(arg, "\"\n") {
		return "", errors.New("ssh_config argument contains a quote or newline")
	}
	arg = strings.ReplaceAll(arg, "%", "%%")
	if strings.ContainsAny(arg, " \t") {
		return `"` + arg + `"`, nil
	}
	return arg, nil
}

func withTrailingNewline(s string) string {
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}
