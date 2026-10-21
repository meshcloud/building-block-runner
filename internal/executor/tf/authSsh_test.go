package tf

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_SshConfig_QuotesPathsAndEscapesTokens(t *testing.T) {
	for name, test := range map[string]struct {
		cfg      sshConfig
		expected string
	}{
		"known hosts": {
			cfg: sshConfig{IdentityFile: "/run dir/ssh/private_key", KnownHostsFiles: []string{"/run dir/ssh/known_hosts", "/home/100%/known_hosts"}},
			expected: "Host *\n  ConnectTimeout 30\n  ServerAliveInterval 15\n  ServerAliveCountMax 4\n  IdentityFile \"/run dir/ssh/private_key\"\n  IdentitiesOnly yes\n" +
				"  UserKnownHostsFile \"/run dir/ssh/known_hosts\" /home/100%%/known_hosts\n  StrictHostKeyChecking yes\n",
		},
		"no known hosts": {
			cfg:      sshConfig{IdentityFile: "/ssh/private_key"},
			expected: "Host *\n  ConnectTimeout 30\n  ServerAliveInterval 15\n  ServerAliveCountMax 4\n  IdentityFile /ssh/private_key\n  IdentitiesOnly yes\n  UserKnownHostsFile none\n  StrictHostKeyChecking yes\n",
		},
		"no key": {
			cfg:      sshConfig{KnownHostsFiles: []string{"/etc/ssh/ssh_known_hosts"}},
			expected: "Host *\n  ConnectTimeout 30\n  ServerAliveInterval 15\n  ServerAliveCountMax 4\n  UserKnownHostsFile /etc/ssh/ssh_known_hosts\n  StrictHostKeyChecking yes\n",
		},
		"insecure": {
			cfg:      sshConfig{IdentityFile: "/ssh/private_key", KnownHostsFiles: []string{"/ssh/known_hosts"}, Insecure: true},
			expected: "Host *\n  ConnectTimeout 30\n  ServerAliveInterval 15\n  ServerAliveCountMax 4\n  IdentityFile /ssh/private_key\n  IdentitiesOnly yes\n  UserKnownHostsFile /dev/null\n  StrictHostKeyChecking no\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), sshConfigFile)

			require.NoError(t, writeSshConfig(path, test.cfg))

			written, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, test.expected, string(written))
		})
	}

	t.Run("refuses a path with a quote", func(t *testing.T) {
		err := writeSshConfig(filepath.Join(t.TempDir(), sshConfigFile), sshConfig{IdentityFile: `/a"b/private_key`})

		assert.ErrorContains(t, err, "ssh_config argument contains a quote or newline")
	})
}
