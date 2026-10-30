package sshgit

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

type Server struct {
	Addr string

	t        *testing.T
	listener net.Listener
	config   *ssh.ServerConfig
	hostKey  ssh.PublicKey

	mu       sync.Mutex
	repoDirs []string
	conns    []net.Conn
	wg       sync.WaitGroup
}

type ClientKey struct {
	PrivateKeyPem string
	PublicKey     ssh.PublicKey
}

func NewClientKey(t *testing.T) ClientKey {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	block, err := ssh.MarshalPrivateKey(private, "")
	require.NoError(t, err)
	publicKey, err := ssh.NewPublicKey(public)
	require.NoError(t, err)
	return ClientKey{PrivateKeyPem: string(pem.EncodeToMemory(block)), PublicKey: publicKey}
}

func Start(t *testing.T, clientKey ssh.PublicKey, repoDirs ...string) *Server {
	t.Helper()
	_, hostPrivate, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	hostSigner, err := ssh.NewSignerFromKey(hostPrivate)
	require.NoError(t, err)
	config := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if !bytes.Equal(key.Marshal(), clientKey.Marshal()) {
				return nil, errors.New("unknown client key")
			}
			return &ssh.Permissions{}, nil
		},
	}
	config.AddHostKey(hostSigner)
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	s := &Server{Addr: listener.Addr().String(), t: t, listener: listener, config: config, hostKey: hostSigner.PublicKey(), repoDirs: repoDirs}
	s.wg.Go(s.accept)
	t.Cleanup(s.stop)
	return s
}

func (s *Server) AddRepository(t *testing.T, repoDir string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.repoDirs = append(s.repoDirs, repoDir)
}

func (s *Server) Url(t *testing.T, repoDir string) string {
	t.Helper()
	return "ssh://git@" + s.Addr + repoDir
}

// DisplayUrl is the URL as the runner logs it, without the user.
func (s *Server) DisplayUrl(t *testing.T, repoDir string) string {
	t.Helper()
	return "ssh://" + s.Addr + repoDir
}

func (s *Server) KnownHost(t *testing.T) map[string]any {
	t.Helper()
	keyType, keyValue, _ := strings.Cut(strings.TrimSpace(string(ssh.MarshalAuthorizedKey(s.hostKey))), " ")
	host, port, err := net.SplitHostPort(s.Addr)
	require.NoError(t, err)
	return map[string]any{"host": "[" + host + "]:" + port, "key_type": keyType, "key_value": keyValue}
}

func (s *Server) serves(repoDir string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Contains(s.repoDirs, repoDir)
}

func (s *Server) stop() {
	_ = s.listener.Close()
	s.mu.Lock()
	for _, conn := range s.conns {
		_ = conn.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
}
