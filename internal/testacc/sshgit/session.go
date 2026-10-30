package sshgit

import (
	"context"
	"io"
	"net"
	"os/exec"
	"strings"

	"github.com/stretchr/testify/assert"
	"golang.org/x/crypto/ssh"
)

func (s *Server) accept() {
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

func (s *Server) serve(conn net.Conn) {
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

func (s *Server) session(channel ssh.Channel, requests <-chan *ssh.Request) {
	defer func() { _ = channel.Close() }()
	for request := range requests {
		if request.Type != "exec" {
			_ = request.Reply(false, nil)
			continue
		}
		var payload struct{ Command string }
		err := ssh.Unmarshal(request.Payload, &payload)
		repoDir, isUploadPack := strings.CutPrefix(payload.Command, "git-upload-pack ")
		repoDir = strings.Trim(repoDir, "'")
		if err != nil || !isUploadPack || !s.serves(repoDir) {
			assert.Failf(s.t, "the SSH git server got an unexpected command", "%q", payload.Command)
			_ = request.Reply(false, nil)
			return
		}
		_ = request.Reply(true, nil)
		s.uploadPack(channel, repoDir)
		return
	}
}

func (s *Server) uploadPack(channel ssh.Channel, repoDir string) {
	// The server outlives t.Context(), because the destroy run in a cleanup clones too.
	cmd := exec.CommandContext(context.WithoutCancel(s.t.Context()), "git-upload-pack", repoDir)
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
