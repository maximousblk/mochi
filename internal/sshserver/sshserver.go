// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

// Package sshserver serves SSH in the Mochi process, over connections that
// the tunnel they arrived on has already authenticated.
//
// No SSH credentials are checked. The SSH username selects the account: a
// session is granted only when the username names the account Mochi runs as,
// and any other username is refused during authentication. Mochi never
// switches accounts, so it cannot serve another user's sessions. Tailscale SSH
// runs as root and can switch to any local account; doing the same here would
// need root (or SYSTEM on Windows), which Mochi does not run with.
//
// Sessions run the user's login shell (PowerShell on Windows) or the command
// the client gave, with a PTY when one is requested. The sftp subsystem serves
// the whole filesystem, with the account's home directory as the working
// directory. Port forwarding and agent forwarding are not offered.
package sshserver

import (
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"os/user"
	"strings"
	"sync/atomic"

	ssh "github.com/tailscale/gliderssh"
	gossh "golang.org/x/crypto/ssh"
)

const sshInteractiveMOTD = "🐈 Connected via Mochi SSH.\r\n"

// HostKey derives this machine's SSH host key from its identity seed, so it
// is stable for the life of the identity and needs no file. The same seed
// always yields the same key.
func HostKey(seed []byte) (gossh.Signer, error) {
	if len(seed) == 0 {
		return nil, errors.New("sshserver: empty host key seed")
	}
	h := sha256.New()
	h.Write([]byte("mochi ssh host key\x00"))
	h.Write(seed)
	return gossh.NewSignerFromKey(ed25519.NewKeyFromSeed(h.Sum(nil)))
}

// Server serves SSH over connections handed to it, in process.
type Server struct {
	srv  *ssh.Server
	logf func(format string, args ...any)
}

// New returns a server presenting hostKey. logf may be nil.
func New(hostKey gossh.Signer, logf func(format string, args ...any)) *Server {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := &Server{logf: logf}
	s.srv = &ssh.Server{
		Handler:             s.sessionHandler,
		NoClientAuthHandler: s.authorize,
		ChannelHandlers:     map[string]ssh.ChannelHandler{"session": ssh.DefaultSessionHandler},
		RequestHandlers:     map[string]ssh.RequestHandler{},
		SubsystemHandlers:   map[string]ssh.SubsystemHandler{"sftp": s.sftpSessionHandler},
	}
	s.srv.AddHostKey(hostKey)
	return s
}

// Handle serves one connection until the client disconnects. It is the
// handler registered on Mochi's Tailcat port for SSH.
func (s *Server) Handle(c net.Conn) {
	s.srv.HandleConn(c)
}

// authorize admits a connection only when its username names the account
// Mochi runs as. It is the only authentication method, so a refused username
// has no other way in.
func (s *Server) authorize(ctx ssh.Context) error {
	u, err := user.Current()
	if err != nil {
		s.logf("ssh: refused user %q: current account: %v", ctx.User(), err)
		return fmt.Errorf("sshserver: current account: %w", err)
	}
	if matchesAccount(ctx.User(), u) {
		return nil
	}
	s.logf("ssh: refused user %q: Mochi runs as %q", ctx.User(), u.Username)
	return errors.New("sshserver: username does not name the account Mochi runs as")
}

// sessionHandler handles a single SSH session (shell or exec).
func (s *Server) sessionHandler(sess ssh.Session) {
	u, err := user.Current()
	if err != nil {
		fmt.Fprintf(sess.Stderr(), "failed to get current user: %v\r\n", err)
		sess.Exit(1)
		return
	}

	// newSessionCommand is per-platform. It returns an unstarted command with
	// Args, Dir, and the base environment set.
	cmd := newSessionCommand(u, sess.RawCommand())
	for _, env := range sess.Environ() {
		if acceptEnvPair(env) {
			cmd.Env = append(cmd.Env, env)
		}
	}

	ptyReq, winCh, isPTY := sess.Pty()
	if isPTY && sess.RawCommand() == "" {
		io.WriteString(sess, sshInteractiveMOTD)
	}
	if isPTY {
		sess.DisablePTYEmulation()
		runWithPTY(sess, cmd, ptyReq, winCh)
	} else {
		runWithPipes(sess, cmd)
	}
}

// runWithPipes runs cmd with stdin/stdout/stderr pipes (no PTY).
func runWithPipes(sess ssh.Session, cmd *exec.Cmd) {
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		fmt.Fprintf(sess.Stderr(), "stdin pipe: %v\r\n", err)
		sess.Exit(1)
		return
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(sess.Stderr(), "stdout pipe: %v\r\n", err)
		sess.Exit(1)
		return
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		fmt.Fprintf(sess.Stderr(), "stderr pipe: %v\r\n", err)
		sess.Exit(1)
		return
	}

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(sess.Stderr(), "start: %v\r\n", err)
		sess.Exit(1)
		return
	}

	go func() {
		defer stdinPipe.Close()
		io.Copy(stdinPipe, sess)
	}()

	outputDone := make(chan struct{})
	var openStreams atomic.Int32
	openStreams.Store(2) // stdout + stderr
	closeOutput := func() {
		if openStreams.Add(-1) == 0 {
			close(outputDone)
		}
	}
	go func() {
		defer closeOutput()
		io.Copy(sess, stdoutPipe)
	}()
	go func() {
		defer closeOutput()
		io.Copy(sess.Stderr(), stderrPipe)
	}()

	// Drain stdout/stderr before calling Wait: Wait closes the pipes
	// once the process exits, racing the copies and sometimes losing
	// the output of fast-exiting commands. (The copies finish on
	// their own: the pipes read EOF when the process exits.)
	<-outputDone
	err = cmd.Wait()

	if err != nil {
		sess.Exit(exitCode(err))
		return
	}
	sess.Exit(0)
}

// exitCode extracts the exit code from an exec error.
func exitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}

// acceptEnvPair reports whether the environment variable key=value pair
// should be accepted from the client (same default as OpenSSH AcceptEnv).
func acceptEnvPair(kv string) bool {
	k, _, ok := strings.Cut(kv, "=")
	if !ok {
		return false
	}
	return k == "TERM" || k == "LANG" || strings.HasPrefix(k, "LC_")
}
