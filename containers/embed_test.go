package containers

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

var errHostKeyRecorded = errors.New("host key recorded")

func TestKnownHostsAreCurrent(t *testing.T) {
	t.Parallel()
	keysByHost := map[string][]ssh.PublicKey{}
	for rest := []byte(KnownHosts); len(bytes.TrimSpace(rest)) > 0; {
		_, hosts, key, _, next, err := ssh.ParseKnownHosts(rest)
		require.NoError(t, err)
		for _, host := range hosts {
			keysByHost[host] = append(keysByHost[host], key)
		}
		rest = next
	}
	require.NotEmpty(t, keysByHost)

	for host, keys := range keysByHost {
		t.Run(host, func(t *testing.T) {
			t.Parallel()
			for _, key := range keys {
				algorithm := hostKeyAlgorithm(key.Type())
				served, err := fetchHostKey(t.Context(), host, algorithm)
				require.NoErrorf(t, err, "%s %s", host, algorithm)
				assert.Truef(t, bytes.Equal(key.Marshal(), served.Marshal()),
					"%s serves a new %s key, replace its line in known_hosts with:\n%s %s",
					host, algorithm, host, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(served))))
			}
		})
	}
}

func hostKeyAlgorithm(keyType string) string {
	if keyType == ssh.KeyAlgoRSA {
		return ssh.KeyAlgoRSASHA512
	}
	return keyType
}

func fetchHostKey(ctx context.Context, host, algorithm string) (ssh.PublicKey, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(host, "22"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	var served ssh.PublicKey
	config := &ssh.ClientConfig{
		User:              "git",
		HostKeyAlgorithms: []string{algorithm},
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			served = key
			return errHostKeyRecorded
		},
	}
	if _, _, _, err := ssh.NewClientConn(conn, host+":22", config); !errors.Is(err, errHostKeyRecorded) {
		return nil, err
	}
	return served, nil
}
