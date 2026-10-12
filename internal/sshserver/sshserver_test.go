// Copyright (c) Tailscale Inc & contributors
// SPDX-License-Identifier: BSD-3-Clause

package sshserver

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkg/sftp"
	gossh "golang.org/x/crypto/ssh"
)

var testSeed = []byte("sshserver test identity")

// logBuf collects the server's log lines.
type logBuf struct {
	mu    sync.Mutex
	lines []string
}

func (l *logBuf) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

// startServer serves SSH on a loopback listener and returns its address and
// host key.
func startServer(t *testing.T, logs *logBuf) (string, gossh.Signer) {
	t.Helper()
	hostKey, err := HostKey(testSeed)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(hostKey, logs.logf)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go srv.Handle(c)
		}
	}()
	return ln.Addr().String(), hostKey
}

// dial connects as username with no credentials, the way the tunnel's
// authenticated peer does.
func dial(t *testing.T, addr string, hostKey gossh.Signer, username string) (*gossh.Client, error) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	cc, chans, reqs, err := gossh.NewClientConn(conn, addr, &gossh.ClientConfig{
		User:            username,
		HostKeyCallback: gossh.FixedHostKey(hostKey.PublicKey()),
		Timeout:         10 * time.Second,
	})
	if err != nil {
		conn.Close()
		return nil, err
	}
	client := gossh.NewClient(cc, chans, reqs)
	t.Cleanup(func() { client.Close() })
	return client, nil
}

func currentUsername(t *testing.T) string {
	t.Helper()
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	return u.Username
}

func TestCurrentAccountRunsCommand(t *testing.T) {
	logs := &logBuf{}
	addr, hostKey := startServer(t, logs)
	client, err := dial(t, addr, hostKey, currentUsername(t))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	sess, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	out, err := sess.Output("echo hi")
	if err != nil {
		t.Fatalf("echo hi: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "hi" {
		t.Fatalf("output = %q, want %q", got, "hi")
	}
}

func TestNonZeroExitStatusReported(t *testing.T) {
	logs := &logBuf{}
	addr, hostKey := startServer(t, logs)
	client, err := dial(t, addr, hostKey, currentUsername(t))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	sess, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	err = sess.Run("exit 3")
	var exitErr *gossh.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("Run(exit 3) error = %v, want *ssh.ExitError", err)
	}
	if got := exitErr.ExitStatus(); got != 3 {
		t.Fatalf("exit status = %d, want 3", got)
	}
}

func TestOtherUsernameRefused(t *testing.T) {
	logs := &logBuf{}
	addr, hostKey := startServer(t, logs)
	other := currentUsername(t) + "-other"
	if _, err := dial(t, addr, hostKey, other); err == nil {
		t.Fatalf("dial as %q succeeded; want refusal", other)
	}
	want := fmt.Sprintf("ssh: refused user %q: Mochi runs as %q", other, currentUsername(t))
	if !strings.Contains(logs.String(), want) {
		t.Fatalf("log = %q, want line %q", logs.String(), want)
	}
}

func TestSFTPSubsystemReadsFile(t *testing.T) {
	logs := &logBuf{}
	addr, hostKey := startServer(t, logs)
	client, err := dial(t, addr, hostKey, currentUsername(t))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	path := filepath.Join(t.TempDir(), "file.txt")
	want := []byte("sftp contents\n")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}

	sess, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	stdin, err := sess.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.RequestSubsystem("sftp"); err != nil {
		t.Fatalf("sftp subsystem: %v", err)
	}
	sc, err := sftp.NewClientPipe(stdout, stdin)
	if err != nil {
		t.Fatal(err)
	}
	defer sc.Close()

	f, err := sc.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	got, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("sftp read %q, want %q", got, want)
	}
}

func TestHostKeyFromSeed(t *testing.T) {
	a, err := HostKey(testSeed)
	if err != nil {
		t.Fatal(err)
	}
	again, err := HostKey(testSeed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.PublicKey().Marshal(), again.PublicKey().Marshal()) {
		t.Fatal("same seed gave different host keys")
	}
	other, err := HostKey([]byte("another identity"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a.PublicKey().Marshal(), other.PublicKey().Marshal()) {
		t.Fatal("different seeds gave the same host key")
	}
}
