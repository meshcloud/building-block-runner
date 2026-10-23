package testacc

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

type sshGitServer struct {
	t        *testing.T
	listener net.Listener
	config   *ssh.ServerConfig
	hostKey  ssh.PublicKey
	repoDir  string

	mu    sync.Mutex
	conns []net.Conn
	wg    sync.WaitGroup
}

type sshClientKey struct {
	privateKeyPem string
	publicKey     ssh.PublicKey
}

func newSshClientKey(t *testing.T) sshClientKey {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	block, err := ssh.MarshalPrivateKey(private, "")
	require.NoError(t, err)
	publicKey, err := ssh.NewPublicKey(public)
	require.NoError(t, err)
	return sshClientKey{privateKeyPem: string(pem.EncodeToMemory(block)), publicKey: publicKey}
}

func startSshGitServer(t *testing.T, repoDir string, clientKey ssh.PublicKey) *sshGitServer {
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

	s := &sshGitServer{t: t, listener: listener, config: config, hostKey: hostSigner.PublicKey(), repoDir: repoDir}
	s.wg.Go(s.accept)
	t.Cleanup(s.stop)
	return s
}

func (s *sshGitServer) url() string {
	return "ssh://git@" + s.listener.Addr().String() + s.repoDir
}

func (s *sshGitServer) displayUrl() string {
	return "ssh://" + s.listener.Addr().String() + s.repoDir
}

func (s *sshGitServer) knownHost() map[string]any {
	keyType, keyValue, _ := strings.Cut(strings.TrimSpace(string(ssh.MarshalAuthorizedKey(s.hostKey))), " ")
	host, port, err := net.SplitHostPort(s.listener.Addr().String())
	require.NoError(s.t, err)
	return map[string]any{"host": "[" + host + "]:" + port, "key_type": keyType, "key_value": keyValue}
}

func (s *sshGitServer) stop() {
	_ = s.listener.Close()
	s.mu.Lock()
	for _, conn := range s.conns {
		_ = conn.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *sshGitServer) accept() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns = append(s.conns, conn)
		s.mu.Unlock()
		s.wg.Go(func() { s.serve(conn) })
	}
}

func (s *sshGitServer) serve(conn net.Conn) {
	serverConn, channels, requests, err := ssh.NewServerConn(conn, s.config)
	if err != nil {
		s.t.Logf("SSH handshake with %s failed: %v", conn.RemoteAddr(), err)
		return
	}
	defer func() { _ = serverConn.Close() }()
	go ssh.DiscardRequests(requests)
	for newChannel := range channels {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "only sessions")
			continue
		}
		channel, channelRequests, err := newChannel.Accept()
		if err != nil {
			return
		}
		s.wg.Go(func() { s.session(channel, channelRequests) })
	}
}

func (s *sshGitServer) session(channel ssh.Channel, requests <-chan *ssh.Request) {
	defer func() { _ = channel.Close() }()
	for request := range requests {
		if request.Type != "exec" {
			_ = request.Reply(false, nil)
			continue
		}
		var payload struct{ Command string }
		if err := ssh.Unmarshal(request.Payload, &payload); err != nil || payload.Command != "git-upload-pack '"+s.repoDir+"'" {
			assert.Failf(s.t, "the SSH git server got an unexpected command", "%q", payload.Command)
			_ = request.Reply(false, nil)
			return
		}
		_ = request.Reply(true, nil)
		s.uploadPack(channel)
		return
	}
}

func (s *sshGitServer) uploadPack(channel ssh.Channel) {
	// The server outlives t.Context(), because the destroy run in a cleanup clones too.
	cmd := exec.CommandContext(context.WithoutCancel(s.t.Context()), "git-upload-pack", s.repoDir)
	cmd.Stdout = channel
	cmd.Stderr = channel.Stderr()
	stdin, err := cmd.StdinPipe()
	if !assert.NoError(s.t, err) {
		return
	}
	if !assert.NoError(s.t, cmd.Start()) {
		return
	}
	// cmd.Wait would wait for the client to close its side, which it does only after the exit status.
	go func() {
		_, _ = io.Copy(stdin, channel)
		_ = stdin.Close()
	}()
	var status uint32
	if err := cmd.Wait(); err != nil {
		status = 1
	}
	_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
}
