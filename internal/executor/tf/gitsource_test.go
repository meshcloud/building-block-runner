package tf

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_IsTransientGitFailure_RetriesOnlyNetworkFailures(t *testing.T) {
	for stderr, transient := range map[string]bool{
		"fatal: unable to access 'https://github.com/o/r/': Could not resolve host: github.com":                        true,
		"error: RPC failed; curl 56 GnuTLS recv error (-54): Error in the pull function.\nfatal: early EOF":            true,
		"fatal: unable to access 'https://github.com/o/r/': The requested URL returned error: 503":                     true,
		"ssh: connect to host github.com port 22: Connection timed out\nfatal: Could not read from remote repository.": true,
		"error: RPC failed; curl 28 Operation too slow. Less than 1000 bytes/sec transferred the last 60 seconds":      true,
		"remote: Repository not found.\nfatal: repository 'https://github.com/o/r/' not found":                         false,
		"git@github.com: Permission denied (publickey).\nfatal: Could not read from remote repository.":                false,
		"Host key verification failed.\nfatal: Could not read from remote repository.":                                 false,
		"fatal: Authentication failed for 'https://github.com/o/r/'":                                                   false,
		"fatal: could not read Username for 'https://github.com': terminal prompts disabled":                           false,
		"fatal: unable to access 'https://github.com/o/r/': The requested URL returned error: 403":                     false,
		"fatal: destination path 'tf' already exists and is not an empty directory.":                                   false,
	} {
		assert.Equal(t, transient, isTransientGitFailure(stderr), stderr)
	}
}
